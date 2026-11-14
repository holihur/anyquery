package engine

import (
	"encoding/gob"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"time"

	"github.com/anyquery/anyquery/sql"
)

const joinSpillPartitions = 16

type joinSpillEntry struct {
	Index int
	Row   map[string]any
}

func init() {
	// Gob needs every concrete type stored in an interface value.
	gob.Register(int(0))
	gob.Register(int64(0))
	gob.Register(float64(0))
	gob.Register("")
	gob.Register(false)
	gob.Register([]any{})
	gob.Register([]string{})
	gob.Register(map[string]any{})
	gob.Register(time.Time{})
	gob.Register(json.Number(""))
}

func joinSpillPartition(key string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return int(h.Sum32() % joinSpillPartitions)
}

// spillHashJoin implements the same hash join as hashJoin but partitions the
// build side to temporary files once it exceeds JoinSpillRows. Probe rows stay
// in memory; the build index for one partition is materialized at a time.
// It returns handled=false when gob cannot encode the build rows, in which case
// the caller safely falls back to the in-memory path.
func (ec *execCtx) spillHashJoin(combined, left, right *relation, typ sql.JoinType, on sql.Expr, pairs [][2]field, leftMatched, rightMatched []bool, buildLeft bool) (bool, error) {
	buildRows := right.rows
	if buildLeft {
		buildRows = left.rows
	}
	dir, err := os.MkdirTemp("", "anyquery-join-*")
	if err != nil {
		return false, err
	}
	defer os.RemoveAll(dir)

	files := make([]*os.File, joinSpillPartitions)
	encoders := make([]*gob.Encoder, joinSpillPartitions)
	cleanup := func() {
		for i := range files {
			if files[i] != nil {
				_ = files[i].Close()
			}
		}
	}
	for i := 0; i < joinSpillPartitions; i++ {
		f, err := os.Create(filepath.Join(dir, fmt.Sprintf("build-%02d.gob", i)))
		if err != nil {
			cleanup()
			return false, err
		}
		files[i] = f
		encoders[i] = gob.NewEncoder(f)
	}
	for idx, row := range buildRows {
		vals := make([]any, len(pairs))
		for i, p := range pairs {
			if buildLeft {
				vals[i] = row[p[0].Key]
			} else {
				vals[i] = row[p[1].Key]
			}
		}
		key, ok := joinKey(vals)
		if !ok {
			continue
		}
		part := joinSpillPartition(key)
		if err := encoders[part].Encode(joinSpillEntry{Index: idx, Row: row}); err != nil {
			cleanup()
			return false, nil
		}
	}
	cleanup()

	probeRows := right.rows
	if buildLeft {
		probeRows = right.rows
	} else {
		probeRows = left.rows
	}
	for part := 0; part < joinSpillPartitions; part++ {
		path := filepath.Join(dir, fmt.Sprintf("build-%02d.gob", part))
		f, err := os.Open(path)
		if err != nil {
			return true, err
		}
		dec := gob.NewDecoder(f)
		entries := make([]joinSpillEntry, 0)
		for {
			var entry joinSpillEntry
			if err := dec.Decode(&entry); err != nil {
				if err.Error() == "EOF" {
					break
				}
				_ = f.Close()
				return true, err
			}
			entries = append(entries, entry)
		}
		_ = f.Close()
		if len(entries) == 0 {
			continue
		}
		index := make(map[string][]int, len(entries))
		for i, entry := range entries {
			vals := make([]any, len(pairs))
			for j, p := range pairs {
				if buildLeft {
					vals[j] = entry.Row[p[0].Key]
				} else {
					vals[j] = entry.Row[p[1].Key]
				}
			}
			key, ok := joinKey(vals)
			if !ok {
				continue
			}
			index[key] = append(index[key], i)
		}
		for pos, probe := range probeRows {
			vals := make([]any, len(pairs))
			for i, p := range pairs {
				if buildLeft {
					vals[i] = probe[p[1].Key]
				} else {
					vals[i] = probe[p[0].Key]
				}
			}
			key, ok := joinKey(vals)
			if !ok {
				continue
			}
			for _, at := range index[key] {
				entry := entries[at]
				li, ri := entry.Index, pos
				if !buildLeft {
					li, ri = pos, entry.Index
				}
				row := mergeRows(left.rows[li], right.rows[ri], combined.fields)
				if on != nil {
					ctx := &evalCtx{rel: combined, row: row, outer: ec.outerEval, exec: ec}
					v, err := evalExpr(ctx, on)
					if err != nil {
						return true, err
					}
					if !truthy(v) {
						continue
					}
				}
				leftMatched[li] = true
				rightMatched[ri] = true
				combined.rows = append(combined.rows, row)
				if len(combined.rows) > ec.engine.Opts.MaxJoinRows {
					return true, &Error{Code: "join_too_large", Message: fmt.Sprintf("join produced more than %d intermediate rows", ec.engine.Opts.MaxJoinRows), Hint: "add join or WHERE conditions"}
				}
			}
		}
	}
	return true, nil
}

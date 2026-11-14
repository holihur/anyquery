package engine

import (
	"fmt"
	"strings"
	"time"
)

// dateValue, timeValue and timestampValue are internal typed temporal values.
// They preserve precision and an explicit/naive zone distinction through
// expressions; Result marshaling converts them to stable SQL text.
type dateValue struct{ t time.Time }

func (v dateValue) String() string { return v.t.Format("2006-01-02") }

type timeValue struct {
	t            time.Time
	precision    int
	precisionSet bool
	zoned        bool
}

func (v timeValue) String() string {
	base := v.t.Format("15:04:05")
	if v.precisionSet {
		if v.precision <= 0 {
			base = v.t.Format("15:04:05")
		} else {
			n := v.precision
			if n > 9 {
				n = 9
			}
			base = v.t.Format("15:04:05." + strings.Repeat("0", n))
		}
	}
	if v.zoned {
		base += v.t.Format("Z07:00")
	}
	return base
}

type timestampValue struct {
	t            time.Time
	precision    int
	precisionSet bool
	naive        bool
}

func (v timestampValue) String() string {
	base := v.t.Format("2006-01-02T15:04:05")
	if v.precisionSet {
		n := v.precision
		if n < 0 {
			n = 0
		}
		if n > 9 {
			n = 9
		}
		if n > 0 {
			frac := fmt.Sprintf("%09d", v.t.Nanosecond())
			base += "." + frac[:n]
		}
	} else {
		base = v.t.Format(time.RFC3339Nano)
		if v.naive {
			base = strings.TrimSuffix(base, "Z")
		}
		return base
	}
	if !v.naive {
		base += v.t.Format("Z07:00")
	}
	return base
}

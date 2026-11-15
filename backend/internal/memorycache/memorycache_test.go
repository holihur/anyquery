package memorycache

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anyquery/anyquery/internal/connector"
)

type mockItem struct {
	key   string
	value []byte
	flags int64
	size  int64
	exp   int64
	la    int64
	cas   int64
}

type mockMemcached struct {
	mu          sync.Mutex
	items       []mockItem
	metadumpErr bool
	gets        [][]string
}

func startMock(t *testing.T, items []mockItem, metadumpErr bool) (string, *mockMemcached) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	m := &mockMemcached{items: items, metadumpErr: metadumpErr}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go m.serve(conn)
		}
	}()
	return ln.Addr().String(), m
}

func (m *mockMemcached) serve(conn net.Conn) {
	defer conn.Close()
	br := bufio.NewReader(conn)
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		switch {
		case line == "version":
			_, _ = io.WriteString(conn, "VERSION 1.6.21\r\n")
		case line == "lru_crawler metadump all":
			if m.metadumpErr {
				_, _ = io.WriteString(conn, "CLIENT_ERROR lru crawler disabled\r\n")
				continue
			}
			for _, it := range m.items {
				_, _ = fmt.Fprintf(conn, "key=%s exp=%d la=%d cas=%d fetch=yes cls=1 size=%d\r\n", url.QueryEscape(it.key), it.exp, it.la, it.cas, it.size)
			}
			_, _ = io.WriteString(conn, "END\r\n")
		case line == "stats items":
			_, _ = fmt.Fprintf(conn, "STAT items:1:number %d\r\nEND\r\n", len(m.items))
		case strings.HasPrefix(line, "stats cachedump "):
			for _, it := range m.items {
				_, _ = fmt.Fprintf(conn, "ITEM %s [%d b; %d s]\r\n", url.QueryEscape(it.key), it.size, it.exp)
			}
			_, _ = io.WriteString(conn, "END\r\n")
		case strings.HasPrefix(line, "get "):
			keys := strings.Fields(strings.TrimPrefix(line, "get "))
			m.mu.Lock()
			m.gets = append(m.gets, keys)
			m.mu.Unlock()
			for _, key := range keys {
				for _, it := range m.items {
					if it.key == key {
						_, _ = fmt.Fprintf(conn, "VALUE %s %d %d\r\n", key, it.flags, len(it.value))
						_, _ = conn.Write(it.value)
						_, _ = io.WriteString(conn, "\r\n")
					}
				}
			}
			_, _ = io.WriteString(conn, "END\r\n")
		default:
			_, _ = io.WriteString(conn, "ERROR\r\n")
		}
	}
}

func defaultItems() []mockItem {
	return []mockItem{
		{key: "user:1", value: []byte(`{"name":"Ada","active":true}`), flags: 0, size: 27, exp: 4102444800, la: 1700000000, cas: 12},
		{key: "user:2", value: []byte("plain text"), flags: 1, size: 10, exp: 0, la: 1700000010, cas: 13},
		{key: "other:1", value: []byte("42"), flags: 0, size: 2, exp: 0, la: 1700000020, cas: 14},
	}
}

func openMock(t *testing.T, addr string, extra map[string]string) connector.Connector {
	t.Helper()
	cfg := map[string]string{"address": addr}
	for k, v := range extra {
		cfg[k] = v
	}
	c, err := connector.Open(context.Background(), "memorycache", cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestMemoryCacheMetadumpFetch(t *testing.T) {
	addr, m := startMock(t, defaultItems(), false)
	c := openMock(t, addr, map[string]string{"key_prefix": "user:", "scan_limit": "100"})
	res, err := c.Fetch(context.Background(), connector.FetchRequest{Table: "entries", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("rows = %d, want 2: %+v", len(res.Rows), res.Rows)
	}
	row := res.Rows[0]
	if row["key"] != "user:1" || row["value"] != `{"name":"Ada","active":true}` || row["value_type"] != "json_object" {
		t.Fatalf("row = %+v", row)
	}
	if row["size"] != int64(27) || row["cas"] != int64(12) || row["flags"] != int64(0) {
		t.Fatalf("metadata = %+v", row)
	}
	if got, ok := row["expires_at"].(time.Time); !ok || got.Unix() != 4102444800 {
		t.Fatalf("expires_at = %#v", row["expires_at"])
	}
	if got, ok := row["last_access"].(time.Time); !ok || got.Unix() != 1700000000 {
		t.Fatalf("last_access = %#v", row["last_access"])
	}
	if len(m.gets) != 1 || strings.Join(m.gets[0], ",") != "user:1,user:2" {
		t.Fatalf("gets = %v", m.gets)
	}
	if res.Truncated {
		t.Fatal("unexpected truncated")
	}
}

func TestMemoryCacheFallbackCachedump(t *testing.T) {
	addr, _ := startMock(t, defaultItems(), true)
	c := openMock(t, addr, map[string]string{"key_prefix": "other:"})
	res, err := c.Fetch(context.Background(), connector.FetchRequest{Table: "entries", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 || res.Rows[0]["key"] != "other:1" || res.Rows[0]["value"] != "42" || res.Rows[0]["value_type"] != "number" {
		t.Fatalf("rows = %+v", res.Rows)
	}
}

func TestMemoryCacheLimitAndBinary(t *testing.T) {
	items := []mockItem{
		{key: "bin", value: []byte{0xff, 0x00, 0x01}, flags: 7, size: 3},
		{key: "second", value: []byte("v"), size: 1},
	}
	addr, _ := startMock(t, items, false)
	c := openMock(t, addr, nil)
	res, err := c.Fetch(context.Background(), connector.FetchRequest{Table: "entries", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 || !res.Truncated {
		t.Fatalf("rows=%d truncated=%v", len(res.Rows), res.Truncated)
	}
	if res.Rows[0]["value_encoding"] != "base64" || res.Rows[0]["value"] != "/wAB" {
		t.Fatalf("binary row = %+v", res.Rows[0])
	}
}

func TestMemoryCacheIntrospectAndErrors(t *testing.T) {
	addr, _ := startMock(t, defaultItems(), false)
	c := openMock(t, addr, map[string]string{"table_name": "cache"})
	tables, err := c.Introspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tables) != 1 || tables[0].Name != "cache" || len(tables[0].Columns) != 9 {
		t.Fatalf("tables = %+v", tables)
	}
	if _, err := c.Fetch(context.Background(), connector.FetchRequest{Table: "wrong", Limit: 1}); err == nil {
		t.Fatal("expected unknown table error")
	}
	if _, err := c.Fetch(context.Background(), connector.FetchRequest{Table: "cache", Limit: 1}); err != nil {
		t.Fatal(err)
	}
	stmt, args, err := c.(connector.StatementDescriber).DescribeFetch(connector.FetchRequest{Table: "cache", Limit: 5})
	if err != nil || !strings.Contains(stmt, "limit=5") || args != nil {
		t.Fatalf("stmt=%q args=%v err=%v", stmt, args, err)
	}
}

func TestMemoryCachePingAndAddress(t *testing.T) {
	addr, _ := startMock(t, nil, false)
	c := openMock(t, addr, nil)
	if err := c.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := connector.Open(context.Background(), "memorycache", map[string]string{"address": "memcached://" + addr}); err != nil {
		t.Fatal(err)
	}
	if _, err := connector.Open(context.Background(), "memorycache", map[string]string{"address": "not-an-address"}); err == nil {
		t.Fatal("expected address validation error")
	}
	if _, err := connector.Open(context.Background(), "memorycache", map[string]string{}); err == nil {
		t.Fatal("expected required address error")
	}
}

func TestValueKind(t *testing.T) {
	for input, want := range map[string]string{
		"":           "empty",
		"null":       "null",
		"true":       "boolean",
		"12.5":       "number",
		`"text"`:     "json_string",
		`{"a":1}`:    "json_object",
		`[1,2]`:      "json_array",
		"plain text": "string",
	} {
		if got := valueKind([]byte(input)); got != want {
			t.Errorf("valueKind(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestParseMetadumpLine(t *testing.T) {
	e, ok := parseMetadumpLine("key=session%3A1 exp=123 la=456 cas=789 fetch=yes cls=1 size=42")
	if !ok || e.key != "session:1" || e.expires.Unix() != 123 || e.lastAccess.Unix() != 456 || e.cas != 789 || e.size != 42 {
		t.Fatalf("entry = %+v ok=%v", e, ok)
	}
	if _, ok := parseMetadumpLine("no key here"); ok {
		t.Fatal("expected invalid line")
	}
}

func TestGetSkipsUnsupportedKeys(t *testing.T) {
	addr, m := startMock(t, defaultItems(), false)
	c := openMock(t, addr, nil).(*Conn)
	out, err := c.get(context.Background(), []entry{{key: "bad key"}, {key: "ok"}, {key: ""}})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 0 {
		t.Fatalf("out = %+v", out)
	}
	if len(m.gets) != 1 || strings.Join(m.gets[0], ",") != "ok" {
		t.Fatalf("gets = %v", m.gets)
	}
}

package connectorpool

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anyquery/anyquery/internal/connector"
)

type fakeConn struct {
	closed  *int32
	fetches int32
}

func (f *fakeConn) Ping(context.Context) error                            { return nil }
func (f *fakeConn) Introspect(context.Context) ([]connector.Table, error) { return nil, nil }
func (f *fakeConn) Fetch(context.Context, connector.FetchRequest) (*connector.FetchResult, error) {
	atomic.AddInt32(&f.fetches, 1)
	return &connector.FetchResult{}, nil
}
func (f *fakeConn) Capabilities() connector.Capabilities { return connector.Capabilities{} }
func (f *fakeConn) Close() error {
	atomic.AddInt32(f.closed, 1)
	return nil
}

func TestPoolReusesExclusiveConnector(t *testing.T) {
	p := New(2, time.Minute)
	var opens, closes int32
	open := func(context.Context) (connector.Connector, error) {
		atomic.AddInt32(&opens, 1)
		return &fakeConn{closed: &closes}, nil
	}
	lease, err := p.Acquire(context.Background(), "t", "s", open)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lease.Fetch(context.Background(), connector.FetchRequest{}); err != nil {
		t.Fatal(err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	if p.Len() != 1 || opens != 1 {
		t.Fatalf("pool len=%d opens=%d", p.Len(), opens)
	}
	lease, err = p.Acquire(context.Background(), "t", "s", open)
	if err != nil {
		t.Fatal(err)
	}
	if opens != 1 {
		t.Fatalf("connector was not reused: opens=%d", opens)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	p.InvalidateTenant("t")
	if p.Len() != 0 || closes != 1 {
		t.Fatalf("len=%d closes=%d", p.Len(), closes)
	}
}

func TestPoolInvalidatesActiveLease(t *testing.T) {
	p := New(2, time.Minute)
	var closes int32
	lease, err := p.Acquire(context.Background(), "t", "s", func(context.Context) (connector.Connector, error) {
		return &fakeConn{closed: &closes}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	p.InvalidateTenant("t")
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	if closes != 1 || p.Len() != 0 {
		t.Fatalf("closes=%d len=%d", closes, p.Len())
	}
}

func TestPoolIdleLimitAndClose(t *testing.T) {
	p := New(1, time.Minute)
	var closes int32
	open := func(context.Context) (connector.Connector, error) {
		return &fakeConn{closed: &closes}, nil
	}
	a, _ := p.Acquire(context.Background(), "t", "s", open)
	b, _ := p.Acquire(context.Background(), "t", "s", open)
	_ = a.Close()
	_ = b.Close()
	if p.Len() != 1 || closes != 1 {
		t.Fatalf("len=%d closes=%d", p.Len(), closes)
	}
	p.CloseIdle()
	if p.Len() != 0 || closes != 2 {
		t.Fatalf("after close len=%d closes=%d", p.Len(), closes)
	}
}

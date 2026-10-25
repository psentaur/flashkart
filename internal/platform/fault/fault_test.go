package fault

import (
	"context"
	"errors"
	"testing"
)

func TestControlledInterruption(t *testing.T) {
	p := New()
	want := errors.New("crash")
	p.Set("after_commit", want, nil)
	if !errors.Is(p.Hit(context.Background(), "after_commit"), want) {
		t.Fatal("failpoint")
	}
	gate := make(chan struct{})
	p.Set("gate", nil, gate)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if p.Hit(ctx, "gate") == nil {
		t.Fatal("cancellation")
	}
	close(gate)
	if p.Hit(context.Background(), "gate") != nil {
		t.Fatal("release")
	}
}

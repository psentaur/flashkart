package clock

import (
	"context"
	"testing"
	"time"
)

func TestManualAdvance(t *testing.T) {
	c := NewManual(time.Unix(0, 0))
	ch := c.After(time.Second)
	c.Advance(time.Second)
	select {
	case <-ch:
	default:
		t.Fatal("timer did not fire")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if Wait(ctx, c, time.Second) == nil {
		t.Fatal("cancellation")
	}
	a, b := UUID(), UUID()
	if a == b {
		t.Fatal("duplicate identity")
	}
}

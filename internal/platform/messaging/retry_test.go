package messaging

import (
	"testing"
	"time"
)

func TestRetryBudget(t *testing.T) {
	for n, want := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second} {
		got, ok := RetryDelay(n)
		if !ok || got != want {
			t.Fatal(n, got)
		}
	}
	if _, ok := RetryDelay(5); ok {
		t.Fatal("sixth retry")
	}
}

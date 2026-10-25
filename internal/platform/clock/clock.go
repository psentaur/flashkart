// Package clock provides explicit time and identity seams for deterministic checks.
package clock

import (
	"context"
	"crypto/rand"
	"fmt"
	"sync"
	"time"
)

type Clock interface {
	Now() time.Time
	After(time.Duration) <-chan time.Time
}
type Real struct{}

func (Real) Now() time.Time                         { return time.Now().UTC() }
func (Real) After(d time.Duration) <-chan time.Time { return time.After(d) }
func Wait(ctx context.Context, c Clock, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.After(d):
		return nil
	}
}

type timer struct {
	at time.Time
	ch chan time.Time
}
type Manual struct {
	mu     sync.Mutex
	now    time.Time
	timers []timer
}

func NewManual(t time.Time) *Manual { return &Manual{now: t.UTC()} }
func (c *Manual) Now() time.Time    { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *Manual) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan time.Time, 1)
	if d <= 0 {
		ch <- c.now
	} else {
		c.timers = append(c.timers, timer{c.now.Add(d), ch})
	}
	return ch
}
func (c *Manual) Advance(d time.Duration) {
	if d < 0 {
		panic("clock cannot move backwards")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	pending := c.timers[:0]
	for _, t := range c.timers {
		if !t.at.After(c.now) {
			t.ch <- t.at
		} else {
			pending = append(pending, t)
		}
	}
	c.timers = pending
}
func UUID() string {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		panic(e)
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}

type IDs interface{ New() string }
type RandomIDs struct{}

func (RandomIDs) New() string { return UUID() }

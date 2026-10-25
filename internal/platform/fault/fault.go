// Package fault provides explicitly controlled interruption boundaries.
package fault

import (
	"context"
	"sync"
)

type Injector interface {
	Hit(context.Context, string) error
}
type None struct{}

func (None) Hit(context.Context, string) error { return nil }

type Point struct {
	mu     sync.Mutex
	errors map[string]error
	gates  map[string]<-chan struct{}
}

func New() *Point { return &Point{errors: map[string]error{}, gates: map[string]<-chan struct{}{}} }
func (p *Point) Set(name string, err error, gate <-chan struct{}) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.errors[name] = err
	p.gates[name] = gate
}
func (p *Point) Hit(ctx context.Context, name string) error {
	p.mu.Lock()
	err, gate := p.errors[name], p.gates[name]
	p.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return err
}

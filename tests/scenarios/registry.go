// Package scenarios supplies a registry now; story phases add isolated business scenarios.
package scenarios

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

type Scenario struct {
	Name, Description string
	Run               func(context.Context) error
}

var registry = struct {
	sync.RWMutex
	items map[string]Scenario
}{items: map[string]Scenario{}}

func Register(s Scenario) {
	registry.Lock()
	defer registry.Unlock()
	if s.Name == "" || s.Run == nil {
		panic("invalid scenario")
	}
	if _, ok := registry.items[s.Name]; ok {
		panic("duplicate scenario")
	}
	registry.items[s.Name] = s
}
func List() []Scenario {
	registry.RLock()
	defer registry.RUnlock()
	out := []Scenario{}
	for _, s := range registry.items {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
func Run(ctx context.Context, name string) error {
	registry.RLock()
	s, ok := registry.items[name]
	registry.RUnlock()
	if !ok {
		return fmt.Errorf("scenario %q unavailable: story fixtures are not implemented", name)
	}
	return s.Run(ctx)
}

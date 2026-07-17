package dataplane

import (
	"context"
	"sync"
	"sync/atomic"
)

type Connections struct {
	mu       sync.Mutex
	next     uint64
	byDevice map[string]map[uint64]context.CancelFunc
	total    atomic.Int64
}

func NewConnections() *Connections {
	return &Connections{byDevice: map[string]map[uint64]context.CancelFunc{}}
}
func (c *Connections) Track(device string, cancel context.CancelFunc) func() {
	c.mu.Lock()
	c.next++
	id := c.next
	if c.byDevice[device] == nil {
		c.byDevice[device] = map[uint64]context.CancelFunc{}
	}
	c.byDevice[device][id] = cancel
	c.total.Add(1)
	c.mu.Unlock()
	return func() {
		c.mu.Lock()
		if _, ok := c.byDevice[device][id]; ok {
			delete(c.byDevice[device], id)
			c.total.Add(-1)
		}
		if len(c.byDevice[device]) == 0 {
			delete(c.byDevice, device)
		}
		c.mu.Unlock()
	}
}
func (c *Connections) Interrupt(device string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	items := c.byDevice[device]
	for _, cancel := range items {
		cancel()
	}
	return len(items)
}
func (c *Connections) Total() int64 { return c.total.Load() }

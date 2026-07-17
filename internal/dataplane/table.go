package dataplane

import (
	"errors"
	"sync"

	"github.com/proxymesh/proxymesh/internal/model"
)

type Table struct {
	mu         sync.RWMutex
	active     map[string]model.DeviceRoute
	byUsername map[string]string
	pending    map[string]model.DeviceRoute
	loaded     bool
}

func NewTable() *Table {
	return &Table{active: map[string]model.DeviceRoute{}, byUsername: map[string]string{}, pending: map[string]model.DeviceRoute{}}
}
func (t *Table) Load(routes []model.DeviceRoute) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.active = map[string]model.DeviceRoute{}
	t.byUsername = map[string]string{}
	for _, r := range routes {
		t.active[r.DeviceID] = r
		t.byUsername[r.DeviceUsername] = r.DeviceID
	}
	t.pending = map[string]model.DeviceRoute{}
	t.loaded = true
}
func (t *Table) Prepare(r model.DeviceRoute) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if current, ok := t.active[r.DeviceID]; ok && r.Version <= current.Version {
		return errors.New("route version is not newer")
	}
	t.pending[r.DeviceID] = r
	return nil
}
func (t *Table) Activate(r model.DeviceRoute) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	pending, ok := t.pending[r.DeviceID]
	if !ok || pending.Version != r.Version {
		return errors.New("route was not prepared")
	}
	pending.Status = model.RouteActive
	t.active[r.DeviceID] = pending
	t.byUsername[pending.DeviceUsername] = pending.DeviceID
	delete(t.pending, r.DeviceID)
	return nil
}
func (t *Table) UpdateDevice(d model.Device) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	r, ok := t.active[d.ID]
	if !ok {
		return nil
	}
	delete(t.byUsername, r.DeviceUsername)
	r.DeviceUsername = d.Username
	r.PasswordHash = d.PasswordHash
	r.IngressPort = d.IngressPort
	r.IngressMethod = d.IngressMethod
	r.IngressPassword = d.IngressPassword
	r.Ingresses = append([]model.DeviceIngress(nil), d.Ingresses...)
	t.active[d.ID] = r
	t.byUsername[d.Username] = d.ID
	return nil
}
func (t *Table) ByUsername(username string) (model.DeviceRoute, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	id, ok := t.byUsername[username]
	if !ok {
		return model.DeviceRoute{}, false
	}
	r, ok := t.active[id]
	return r, ok
}
func (t *Table) ByDevice(id string) (model.DeviceRoute, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	r, ok := t.active[id]
	return r, ok
}
func (t *Table) Snapshot() []model.DeviceRoute {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]model.DeviceRoute, 0, len(t.active))
	for _, r := range t.active {
		out = append(out, r)
	}
	return out
}
func (t *Table) Loaded() bool { t.mu.RLock(); defer t.mu.RUnlock(); return t.loaded }
func (t *Table) MaxVersion() int64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	var max int64
	for _, r := range t.active {
		if r.Version > max {
			max = r.Version
		}
	}
	return max
}

package store

import (
	"context"
	"sync"
	"time"

	"github.com/proxymesh/proxymesh/internal/model"
	"github.com/proxymesh/proxymesh/internal/provider"
)

type Memory struct {
	mu        sync.RWMutex
	devices   map[string]model.Device
	routes    map[string]model.DeviceRoute
	pending   map[string]model.DeviceRoute
	gateways  map[string]model.Gateway
	providers map[string]provider.Definition
}

func NewMemory() *Memory {
	return &Memory{devices: map[string]model.Device{}, routes: map[string]model.DeviceRoute{}, pending: map[string]model.DeviceRoute{}, gateways: map[string]model.Gateway{}, providers: map[string]provider.Definition{}}
}

func (m *Memory) UpsertProvider(_ context.Context, d provider.Definition) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.providers[d.ID] = cloneProvider(d)
	return nil
}
func (m *Memory) GetProvider(_ context.Context, id string) (provider.Definition, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	d, ok := m.providers[id]
	if !ok {
		return d, ErrNotFound
	}
	return cloneProvider(d), nil
}
func (m *Memory) ListProviders(_ context.Context) ([]provider.Definition, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]provider.Definition, 0, len(m.providers))
	for _, d := range m.providers {
		out = append(out, cloneProvider(d))
	}
	return out, nil
}
func cloneProvider(d provider.Definition) provider.Definition {
	d.Secrets = cloneStrings(d.Secrets)
	return d
}
func cloneStrings(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func (m *Memory) CreateDevice(_ context.Context, d model.Device) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.devices[d.ID]; ok {
		return ErrConflict
	}
	for _, existing := range m.devices {
		for _, ingress := range existing.EffectiveIngresses() {
			if ingress.Port == d.IngressPort {
				return ErrConflict
			}
		}
	}
	if len(d.Ingresses) == 0 && d.IngressPort != 0 {
		d.Ingresses = d.EffectiveIngresses()
	}
	m.devices[d.ID] = d
	return nil
}
func (m *Memory) GetDevice(_ context.Context, id string) (model.Device, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	d, ok := m.devices[id]
	if !ok {
		return d, ErrNotFound
	}
	return d, nil
}
func (m *Memory) UpdateDeviceCredential(_ context.Context, id, password string) (model.Device, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.devices[id]
	if !ok {
		return d, ErrNotFound
	}
	d.IngressPassword = password
	for i := range d.Ingresses {
		if d.Ingresses[i].Primary {
			d.Ingresses[i].Password = password
		}
	}
	d.UpdatedAt = time.Now().UTC()
	m.devices[id] = d
	if r, ok := m.routes[id]; ok {
		r.IngressPassword = password
		m.routes[id] = r
	}
	if r, ok := m.pending[id]; ok {
		r.IngressPassword = password
		m.pending[id] = r
	}
	return d, nil
}
func (m *Memory) AddDeviceIngress(_ context.Context, id string, ingress model.DeviceIngress) (model.Device, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.devices[id]
	if !ok {
		return d, ErrNotFound
	}
	for _, existing := range m.devices {
		for _, item := range existing.EffectiveIngresses() {
			if item.Port == ingress.Port {
				return d, ErrConflict
			}
		}
	}
	ingress.Primary = false
	d.Ingresses = append(d.EffectiveIngresses(), ingress)
	d.UpdatedAt = time.Now().UTC()
	m.devices[id] = d
	if r, ok := m.routes[id]; ok {
		r.Ingresses = append([]model.DeviceIngress(nil), d.Ingresses...)
		m.routes[id] = r
	}
	if r, ok := m.pending[id]; ok {
		r.Ingresses = append([]model.DeviceIngress(nil), d.Ingresses...)
		m.pending[id] = r
	}
	return d, nil
}
func (m *Memory) DeleteDeviceIngress(_ context.Context, id string, port int) (model.Device, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.devices[id]
	if !ok {
		return d, ErrNotFound
	}
	ingresses := d.EffectiveIngresses()
	if len(ingresses) <= 1 {
		return d, ErrConflict
	}
	removed := false
	removedPrimary := false
	out := make([]model.DeviceIngress, 0, len(ingresses)-1)
	for _, ingress := range ingresses {
		if ingress.Port == port {
			removed = true
			removedPrimary = ingress.Primary
			continue
		}
		out = append(out, ingress)
	}
	if !removed {
		return d, ErrNotFound
	}
	if removedPrimary {
		out[0].Primary = true
		d.IngressPort, d.IngressMethod, d.IngressPassword = out[0].Port, out[0].Method, out[0].Password
	}
	d.Ingresses = out
	d.UpdatedAt = time.Now().UTC()
	m.devices[id] = d
	if r, ok := m.routes[id]; ok {
		r.IngressPort, r.IngressMethod, r.IngressPassword = d.IngressPort, d.IngressMethod, d.IngressPassword
		r.Ingresses = append([]model.DeviceIngress(nil), out...)
		m.routes[id] = r
	}
	if r, ok := m.pending[id]; ok {
		r.IngressPort, r.IngressMethod, r.IngressPassword = d.IngressPort, d.IngressMethod, d.IngressPassword
		r.Ingresses = append([]model.DeviceIngress(nil), out...)
		m.pending[id] = r
	}
	return d, nil
}
func (m *Memory) StageRoute(_ context.Context, r model.DeviceRoute, expected int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	current := m.routes[r.DeviceID].Version
	if current != expected {
		return ErrConflict
	}
	m.pending[r.DeviceID] = r
	return nil
}
func (m *Memory) ActivateRoute(_ context.Context, id string, version int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.pending[id]
	if !ok || r.Version != version {
		return ErrConflict
	}
	r.Status = model.RouteActive
	m.routes[id] = r
	delete(m.pending, id)
	return nil
}
func (m *Memory) GetRoute(_ context.Context, id string) (model.DeviceRoute, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	r, ok := m.routes[id]
	if !ok {
		return r, ErrNotFound
	}
	return r, nil
}
func (m *Memory) ListActiveRoutes(_ context.Context) ([]model.DeviceRoute, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]model.DeviceRoute, 0, len(m.routes))
	for _, r := range m.routes {
		out = append(out, r)
	}
	return out, nil
}
func (m *Memory) UpsertGateway(_ context.Context, g model.Gateway) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gateways[g.ID] = g
	return nil
}
func (m *Memory) ListGateways(_ context.Context) ([]model.Gateway, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]model.Gateway, 0, len(m.gateways))
	for _, g := range m.gateways {
		out = append(out, g)
	}
	return out, nil
}
func (m *Memory) AppendAudit(context.Context, string, string, string, map[string]any) error {
	return nil
}
func (m *Memory) RecordRouteDeployment(context.Context, string, int64, string, string, bool, string) error {
	return nil
}
func (m *Memory) Close() {}

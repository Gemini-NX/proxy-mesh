package control

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/proxymesh/proxymesh/internal/controlrpc"
	"github.com/proxymesh/proxymesh/internal/model"
	"github.com/proxymesh/proxymesh/internal/store"
)

type session struct {
	id    string
	send  chan *controlrpc.Frame
	acks  chan *controlrpc.Frame
	done  chan struct{}
	ready atomic.Bool
}
type Hub struct {
	store    store.Store
	mu       sync.RWMutex
	sessions map[string]*session
	deployMu sync.Mutex
	timeout  time.Duration
}

func NewHub(s store.Store, timeout time.Duration) *Hub {
	return &Hub{store: s, sessions: map[string]*session{}, timeout: timeout}
}

func (h *Hub) Connect(stream controlrpc.GatewayControl_ConnectServer) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	if first.Type != controlrpc.TypeRegister || first.GatewayID == "" {
		return errors.New("first frame must register gateway")
	}
	s := &session{id: first.GatewayID, send: make(chan *controlrpc.Frame, 32), acks: make(chan *controlrpc.Frame, 32), done: make(chan struct{})}
	h.mu.Lock()
	h.sessions[s.id] = s
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		if h.sessions[s.id] == s {
			delete(h.sessions, s.id)
		}
		h.mu.Unlock()
		close(s.done)
	}()
	routes, err := h.store.ListActiveRoutes(stream.Context())
	if err != nil {
		return err
	}
	if err := stream.Send(&controlrpc.Frame{Type: controlrpc.TypeSnapshot, RequestID: newID(), Routes: routes}); err != nil {
		return err
	}
	recvErr := make(chan error, 1)
	go func() {
		for {
			f, e := stream.Recv()
			if e != nil {
				recvErr <- e
				return
			}
			switch f.Type {
			case controlrpc.TypeAck:
				s.acks <- f
			case controlrpc.TypeHeartbeat:
				if f.Heartbeat != nil {
					f.Heartbeat.ID = s.id
					f.Heartbeat.LastHeartbeatAt = time.Now().UTC()
					s.ready.Store(f.Heartbeat.Status == model.GatewayReady)
					_ = h.store.UpsertGateway(context.Background(), *f.Heartbeat)
				}
			}
		}
	}()
	for {
		select {
		case f := <-s.send:
			if err := stream.Send(f); err != nil {
				return err
			}
		case err := <-recvErr:
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		case <-s.done:
			return nil
		case <-stream.Context().Done():
			return stream.Context().Err()
		}
	}
}

func (h *Hub) DeployRoute(ctx context.Context, r model.DeviceRoute, commit func() error) error {
	h.deployMu.Lock()
	defer h.deployMu.Unlock()
	sessions := h.readySessions()
	if len(sessions) == 0 {
		return errors.New("no ready gateways connected")
	}
	requestID := newID()
	if err := h.broadcastAndWait(ctx, sessions, &controlrpc.Frame{Type: controlrpc.TypePrepare, RequestID: requestID, Route: &r}); err != nil {
		return fmt.Errorf("prepare route: %w", err)
	}
	if err := commit(); err != nil {
		return fmt.Errorf("commit route: %w", err)
	}
	return h.broadcastAndWait(ctx, sessions, &controlrpc.Frame{Type: controlrpc.TypeActivate, RequestID: requestID, Route: &r})
}
func (h *Hub) BroadcastDevice(ctx context.Context, d model.Device) error {
	return h.broadcastAndWait(ctx, h.readySessions(), &controlrpc.Frame{Type: controlrpc.TypeDeviceUpdate, RequestID: newID(), Device: &d})
}
func (h *Hub) DeployDevice(ctx context.Context, d model.Device, commit func() error) error {
	h.deployMu.Lock()
	defer h.deployMu.Unlock()
	sessions := h.readySessions()
	if len(sessions) == 0 {
		return errors.New("no ready gateways connected")
	}
	requestID := newID()
	if err := h.broadcastAndWait(ctx, sessions, &controlrpc.Frame{Type: controlrpc.TypeDevicePrepare, RequestID: requestID, Device: &d}); err != nil {
		return fmt.Errorf("prepare device ingress: %w", err)
	}
	if err := commit(); err != nil {
		return fmt.Errorf("commit device ingress: %w", err)
	}
	return h.broadcastAndWait(ctx, sessions, &controlrpc.Frame{Type: controlrpc.TypeDeviceUpdate, RequestID: requestID, Device: &d})
}
func (h *Hub) Interrupt(ctx context.Context, deviceID string) error {
	return h.broadcastAndWait(ctx, h.readySessions(), &controlrpc.Frame{Type: controlrpc.TypeInterrupt, RequestID: newID(), DeviceID: deviceID})
}
func (h *Hub) SetDraining(ctx context.Context, gatewayID string, draining bool) error {
	h.mu.RLock()
	s := h.sessions[gatewayID]
	h.mu.RUnlock()
	if s == nil {
		return store.ErrNotFound
	}
	err := h.broadcastAndWait(ctx, []*session{s}, &controlrpc.Frame{Type: controlrpc.TypeSetDraining, RequestID: newID(), DeviceID: fmt.Sprint(draining)})
	s.ready.Store(false)
	return err
}
func (h *Hub) readySessions() []*session {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]*session, 0, len(h.sessions))
	for _, s := range h.sessions {
		if s.ready.Load() {
			out = append(out, s)
		}
	}
	return out
}
func (h *Hub) broadcastAndWait(ctx context.Context, sessions []*session, f *controlrpc.Frame) error {
	if len(sessions) == 0 {
		return errors.New("no ready gateways connected")
	}
	for _, s := range sessions {
		select {
		case s.send <- f:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	deadline := time.NewTimer(h.timeout)
	defer deadline.Stop()
	for _, s := range sessions {
		for {
			select {
			case ack := <-s.acks:
				if ack.RequestID != f.RequestID {
					continue
				}
				if ack.Error != "" {
					h.recordDeployment(f, s.id, false, ack.Error)
					return fmt.Errorf("gateway %s: %s", s.id, ack.Error)
				}
				h.recordDeployment(f, s.id, true, "")
				goto next
			case <-deadline.C:
				h.recordDeployment(f, s.id, false, "acknowledgement timeout")
				return fmt.Errorf("gateway %s acknowledgement timeout", s.id)
			case <-ctx.Done():
				return ctx.Err()
			case <-s.done:
				h.recordDeployment(f, s.id, false, "gateway disconnected")
				return fmt.Errorf("gateway %s disconnected", s.id)
			}
		}
	next:
	}
	return nil
}
func (h *Hub) recordDeployment(f *controlrpc.Frame, gatewayID string, success bool, message string) {
	if f.Route == nil || (f.Type != controlrpc.TypePrepare && f.Type != controlrpc.TypeActivate) {
		return
	}
	_ = h.store.RecordRouteDeployment(context.Background(), f.Route.DeviceID, f.Route.Version, gatewayID, f.Type, success, message)
}
func newID() string { return fmt.Sprintf("%d", time.Now().UnixNano()) }

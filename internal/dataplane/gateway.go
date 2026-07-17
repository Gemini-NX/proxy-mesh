package dataplane

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	sscore "github.com/go-gost/go-shadowsocks2/core"
	sssocks "github.com/go-gost/go-shadowsocks2/socks"
	ssutils "github.com/go-gost/go-shadowsocks2/utils"
	"github.com/proxymesh/proxymesh/internal/model"
	proxyclient "github.com/proxymesh/proxymesh/internal/proxy"
	"github.com/proxymesh/proxymesh/internal/ssconfig"
)

type deviceListener struct {
	deviceID, method, password string
	listener                   net.Listener
	server                     sscore.TCPServer
	active                     atomic.Bool
}
type Gateway struct {
	Table         *Table
	Connections   *Connections
	DialTimeout   time.Duration
	Logger        *slog.Logger
	draining      atomic.Bool
	canaryOK      atomic.Bool
	controlSynced atomic.Bool
	listenHost    string
	listenersMu   sync.Mutex
	listeners     map[int]*deviceListener
	connectOK     atomic.Uint64
	connectFailed atomic.Uint64
}

func NewGateway(t *Table, c *Connections, timeout time.Duration, l *slog.Logger) *Gateway {
	g := &Gateway{Table: t, Connections: c, DialTimeout: timeout, Logger: l, listenHost: "0.0.0.0", listeners: map[int]*deviceListener{}}
	g.canaryOK.Store(true)
	return g
}
func (g *Gateway) SetListenHost(host string) { g.listenHost = host }
func (g *Gateway) SetDraining(v bool)        { g.draining.Store(v) }
func (g *Gateway) SetCanary(v bool)          { g.canaryOK.Store(v) }
func (g *Gateway) SetControlSynced(v bool)   { g.controlSynced.Store(v) }
func (g *Gateway) Ready() bool {
	return g.Table.Loaded() && g.controlSynced.Load() && !g.draining.Load() && g.canaryOK.Load()
}
func (g *Gateway) HealthHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /live", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200); _, _ = w.Write([]byte("ok\n")) })
	mux.HandleFunc("GET /ready", func(w http.ResponseWriter, _ *http.Request) {
		if !g.Ready() {
			http.Error(w, "not ready", 503)
			return
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte("ready\n"))
	})
	mux.HandleFunc("GET /metrics", g.metrics)
	mux.HandleFunc("POST /drain", func(w http.ResponseWriter, r *http.Request) {
		if !requestFromLoopback(r) {
			http.Error(w, "local access only", http.StatusForbidden)
			return
		}
		g.SetDraining(true)
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /undrain", func(w http.ResponseWriter, r *http.Request) {
		if !requestFromLoopback(r) {
			http.Error(w, "local access only", http.StatusForbidden)
			return
		}
		g.SetDraining(false)
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
}

func requestFromLoopback(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	return err == nil && net.ParseIP(host).IsLoopback()
}
func (g *Gateway) ReconcileListeners() error {
	desired := make(map[int]model.DeviceRoute)
	for _, route := range g.Table.Snapshot() {
		for _, ingress := range route.EffectiveIngresses() {
			if ingress.Method == "" || ingress.Password == "" {
				return fmt.Errorf("device %s has incomplete Shadowsocks ingress", route.DeviceID)
			}
			if _, exists := desired[ingress.Port]; exists {
				return fmt.Errorf("duplicate Shadowsocks ingress port %d", ingress.Port)
			}
			item := route
			item.IngressPort, item.IngressMethod, item.IngressPassword = ingress.Port, ingress.Method, ingress.Password
			desired[ingress.Port] = item
		}
	}
	g.listenersMu.Lock()
	defer g.listenersMu.Unlock()
	for port, current := range g.listeners {
		route, keep := desired[port]
		if keep && current.deviceID == route.DeviceID && current.method == route.IngressMethod && current.password == route.IngressPassword {
			current.active.Store(true)
			delete(desired, port)
			continue
		}
		_ = current.listener.Close()
		delete(g.listeners, port)
	}
	for _, route := range desired {
		if err := g.startListenerLocked(route, true); err != nil {
			return err
		}
	}
	return nil
}

func (g *Gateway) PrepareDevice(d model.Device) error {
	g.listenersMu.Lock()
	defer g.listenersMu.Unlock()
	for _, ingress := range d.EffectiveIngresses() {
		if current, ok := g.listeners[ingress.Port]; ok {
			if current.deviceID != d.ID {
				return fmt.Errorf("Shadowsocks ingress port %d is already reserved", ingress.Port)
			}
			route := model.DeviceRoute{DeviceID: d.ID, IngressPort: ingress.Port, IngressMethod: ingress.Method, IngressPassword: ingress.Password}
			if err := g.ValidateRoute(route); err != nil {
				return err
			}
			continue
		}
		route := model.DeviceRoute{DeviceID: d.ID, IngressPort: ingress.Port, IngressMethod: ingress.Method, IngressPassword: ingress.Password}
		if err := g.ValidateRoute(route); err != nil {
			return err
		}
		if err := g.startListenerLocked(route, false); err != nil {
			return err
		}
	}
	return nil
}

func (g *Gateway) PrepareListener(route model.DeviceRoute) error {
	return g.PrepareDevice(model.Device{ID: route.DeviceID, IngressPort: route.IngressPort, IngressMethod: route.IngressMethod, IngressPassword: route.IngressPassword, Ingresses: route.Ingresses})
}

func (g *Gateway) startListenerLocked(route model.DeviceRoute, active bool) error {
	if err := ssconfig.Validate(route.IngressMethod, route.IngressPassword); err != nil {
		return fmt.Errorf("device %s Shadowsocks credentials: %w", route.DeviceID, err)
	}
	serverConfig, err := ssutils.NewServerConfig(route.IngressMethod, route.IngressPassword, nil)
	if err != nil {
		return fmt.Errorf("device %s Shadowsocks method: %w", route.DeviceID, err)
	}
	server := sscore.NewTCPServer(serverConfig)
	if err = server.Init(); err != nil {
		return fmt.Errorf("device %s Shadowsocks server: %w", route.DeviceID, err)
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(g.listenHost, fmt.Sprint(route.IngressPort)))
	if err != nil {
		return fmt.Errorf("listen for device %s on port %d: %w", route.DeviceID, route.IngressPort, err)
	}
	item := &deviceListener{deviceID: route.DeviceID, method: route.IngressMethod, password: route.IngressPassword, listener: listener, server: server}
	item.active.Store(active)
	g.listeners[route.IngressPort] = item
	go g.acceptLoop(item)
	g.Logger.Info("Shadowsocks ingress listening", "deviceId", route.DeviceID, "port", route.IngressPort, "method", route.IngressMethod)
	return nil
}

func (g *Gateway) ValidateRoute(route model.DeviceRoute) error {
	if route.IngressPort < 1 || route.IngressPort > 65535 {
		return fmt.Errorf("invalid Shadowsocks ingress port")
	}
	if route.IngressPassword == "" {
		return fmt.Errorf("missing Shadowsocks ingress password")
	}
	return ssconfig.Validate(route.IngressMethod, route.IngressPassword)
}

func (g *Gateway) CloseListeners() {
	g.listenersMu.Lock()
	defer g.listenersMu.Unlock()
	for port, item := range g.listeners {
		_ = item.listener.Close()
		delete(g.listeners, port)
	}
}

func (g *Gateway) acceptLoop(item *deviceListener) {
	for {
		conn, err := item.listener.Accept()
		if err != nil {
			return
		}
		client, err := item.server.WrapConn(conn)
		if err != nil {
			_ = conn.Close()
			continue
		}
		go g.handleShadowsocks(item, client, conn)
	}
}

func (g *Gateway) handleShadowsocks(item *deviceListener, client net.Conn, raw net.Conn) {
	defer raw.Close()
	if !g.Ready() || !item.active.Load() {
		return
	}
	deviceID := item.deviceID
	route, ok := g.Table.ByDevice(deviceID)
	if !ok {
		return
	}
	if route.Credential.ExpiresAt != nil && time.Now().After(*route.Credential.ExpiresAt) {
		return
	}
	_ = raw.SetReadDeadline(time.Now().Add(g.DialTimeout))
	targeted, ok := client.(interface{ Target() sssocks.Addr })
	if !ok {
		g.connectFailed.Add(1)
		g.Logger.Error("Shadowsocks connection does not expose a target", "deviceId", deviceID)
		return
	}
	target := targeted.Target()
	if target == nil {
		g.connectFailed.Add(1)
		g.Logger.Warn("Shadowsocks handshake failed", "deviceId", deviceID)
		return
	}
	_ = raw.SetReadDeadline(time.Time{})
	ctx, cancel := context.WithCancel(context.Background())
	untrack := g.Connections.Track(route.DeviceID, cancel)
	defer func() { cancel(); untrack() }()
	dialCtx, dialCancel := context.WithTimeout(ctx, g.DialTimeout)
	upstream, err := proxyclient.DialSOCKS5(dialCtx, proxyclient.SOCKS5Config{Address: net.JoinHostPort(route.Credential.Host, fmt.Sprint(route.Credential.Port)), Username: route.Credential.Username, Password: route.Credential.Password}, target.String())
	dialCancel()
	if err != nil {
		g.connectFailed.Add(1)
		g.Logger.Warn("upstream connection failed", "deviceId", route.DeviceID, "routeVersion", route.Version, "error", err)
		return
	}
	defer upstream.Close()
	g.connectOK.Add(1)
	relay(ctx, client, upstream)
}
func relay(ctx context.Context, a, b net.Conn) {
	done := make(chan struct{}, 2)
	copyOne := func(dst, src net.Conn) {
		_, _ = io.Copy(dst, src)
		if tcp, ok := dst.(*net.TCPConn); ok {
			_ = tcp.CloseWrite()
		}
		done <- struct{}{}
	}
	go copyOne(a, b)
	go copyOne(b, a)
	select {
	case <-ctx.Done():
		_ = a.Close()
		_ = b.Close()
	case <-done:
		<-done
	}
}
func (g *Gateway) metrics(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	ready := 0
	if g.Ready() {
		ready = 1
	}
	_, _ = fmt.Fprintf(w, "proxymesh_gateway_ready %d\nproxymesh_active_connections %d\nproxymesh_connect_success_total %d\nproxymesh_connect_failure_total %d\n", ready, g.Connections.Total(), g.connectOK.Load(), g.connectFailed.Load())
}

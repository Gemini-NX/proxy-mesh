package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/proxymesh/proxymesh/internal/config"
	"github.com/proxymesh/proxymesh/internal/cryptox"
	"github.com/proxymesh/proxymesh/internal/dataplane"
	proxyclient "github.com/proxymesh/proxymesh/internal/proxy"
)

func main() {
	if err := run(); err != nil {
		slog.Error("gateway stopped", "error", err)
		os.Exit(1)
	}
}
func run() error {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	cipher, err := cryptox.New(config.String("SNAPSHOT_KEY", config.String("ENCRYPTION_KEY", "")))
	if err != nil {
		return err
	}
	table := dataplane.NewTable()
	snapshotPath := config.String("SNAPSHOT_PATH", "./data/routes.snapshot")
	if routes, e := dataplane.LoadSnapshot(snapshotPath, cipher); e == nil {
		table.Load(routes)
		logger.Info("loaded route snapshot", "routes", len(routes))
	} else if !errors.Is(e, os.ErrNotExist) {
		logger.Warn("route snapshot unavailable", "error", e)
	}
	connections := dataplane.NewConnections()
	gateway := dataplane.NewGateway(table, connections, config.Duration("SOCKS_DIAL_TIMEOUT", 10*time.Second), logger)
	gateway.SetListenHost(config.String("SHADOWSOCKS_LISTEN_HOST", "0.0.0.0"))
	if table.Loaded() {
		if err = gateway.ReconcileListeners(); err != nil {
			return err
		}
	}
	requireCanary := config.Bool("REQUIRE_CANARY", false)
	if requireCanary {
		gateway.SetCanary(false)
	}
	client := &dataplane.ControlClient{GatewayID: config.String("GATEWAY_ID", hostname()), Address: config.String("GATEWAY_ADDRESS", ""), ControlAddress: config.String("CONTROL_GRPC_ADDR", "127.0.0.1:9090"), SnapshotPath: snapshotPath, Table: table, Gateway: gateway, Cipher: cipher, Logger: logger, TLSCA: os.Getenv("CONTROL_TLS_CA"), TLSCert: os.Getenv("CONTROL_TLS_CERT"), TLSKey: os.Getenv("CONTROL_TLS_KEY"), TLSServerName: os.Getenv("CONTROL_TLS_SERVER_NAME")}
	go client.Run(ctx)
	go canaryLoop(ctx, gateway, table, requireCanary, logger)
	healthServer := &http.Server{Addr: config.String("HEALTH_ADDR", ":18080"), Handler: gateway.HealthHandler(), ReadHeaderTimeout: 3 * time.Second, MaxHeaderBytes: 8 << 10}
	errCh := make(chan error, 1)
	go func() {
		logger.Info("health listening", "address", healthServer.Addr)
		if e := healthServer.ListenAndServe(); !errors.Is(e, http.ErrServerClosed) {
			errCh <- e
		}
	}()
	select {
	case <-ctx.Done():
	case err = <-errCh:
		stop()
	}
	gateway.SetDraining(true)
	gateway.CloseListeners()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = healthServer.Shutdown(shutdownCtx)
	return err
}
func canaryLoop(ctx context.Context, g *dataplane.Gateway, t *dataplane.Table, required bool, l *slog.Logger) {
	deviceID, target := os.Getenv("CANARY_DEVICE_ID"), config.String("CANARY_TARGET", "1.1.1.1:443")
	ticker := time.NewTicker(config.Duration("CANARY_INTERVAL", 15*time.Second))
	defer ticker.Stop()
	check := func() {
		if !required {
			g.SetCanary(true)
			return
		}
		route, ok := t.ByDevice(deviceID)
		if !ok {
			g.SetCanary(false)
			return
		}
		checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		conn, err := proxyclient.DialSOCKS5(checkCtx, proxyclient.SOCKS5Config{Address: net.JoinHostPort(route.Credential.Host, itoa(route.Credential.Port)), Username: route.Credential.Username, Password: route.Credential.Password}, target)
		if err == nil {
			conn.Close()
			g.SetCanary(true)
		} else {
			g.SetCanary(false)
			l.Warn("canary failed", "deviceId", deviceID, "error", err)
		}
	}
	check()
	for {
		select {
		case <-ticker.C:
			check()
		case <-ctx.Done():
			return
		}
	}
}
func hostname() string {
	h, _ := os.Hostname()
	if h == "" {
		return "gateway-unknown"
	}
	return h
}
func itoa(v int) string {
	const digits = "0123456789"
	if v == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = digits[v%10]
		v /= 10
	}
	return string(b[i:])
}

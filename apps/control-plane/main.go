package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proxymesh/proxymesh/db/migrations"
	"github.com/proxymesh/proxymesh/internal/config"
	"github.com/proxymesh/proxymesh/internal/control"
	"github.com/proxymesh/proxymesh/internal/controlrpc"
	"github.com/proxymesh/proxymesh/internal/cryptox"
	"github.com/proxymesh/proxymesh/internal/store"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

func main() {
	if err := run(); err != nil {
		slog.Error("control plane stopped", "error", err)
		os.Exit(1)
	}
}
func run() error {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	key := config.String("ENCRYPTION_KEY", "")
	cipher, err := cryptox.New(key)
	if err != nil {
		return err
	}
	databaseURL := config.String("DATABASE_URL", "")
	if databaseURL == "" {
		return errors.New("DATABASE_URL is required")
	}
	if config.Bool("AUTO_MIGRATE", false) {
		pool, e := pgxpool.New(ctx, databaseURL)
		if e != nil {
			return e
		}
		if e = store.ApplyMigrations(ctx, pool, migrations.All); e != nil {
			pool.Close()
			return e
		}
		pool.Close()
	}
	db, err := store.NewPostgres(ctx, databaseURL, cipher)
	if err != nil {
		return err
	}
	defer db.Close()
	hub := control.NewHub(db, config.Duration("GATEWAY_ACK_TIMEOUT", 10*time.Second))
	grpcOptions := []grpc.ServerOption{grpc.ForceServerCodec(controlrpc.Codec{})}
	if tlsConfig, err := serverTLS(); err != nil {
		return err
	} else if tlsConfig != nil {
		grpcOptions = append(grpcOptions, grpc.Creds(credentials.NewTLS(tlsConfig)))
	}
	grpcServer := grpc.NewServer(grpcOptions...)
	controlrpc.RegisterGatewayControlServer(grpcServer, hub)
	grpcLis, err := net.Listen("tcp", config.String("GRPC_ADDR", ":9090"))
	if err != nil {
		return err
	}
	go func() {
		logger.Info("gRPC listening", "address", grpcLis.Addr())
		if e := grpcServer.Serve(grpcLis); e != nil {
			logger.Error("gRPC server", "error", e)
		}
	}()
	api := control.NewAPI(db, hub, config.String("ADMIN_TOKEN", ""), config.String("PUBLIC_PROXY_HOST", "proxy.example.com"), logger)
	api.SetIngressPortRange(config.Int("INGRESS_PORT_START", 50000), config.Int("INGRESS_PORT_END", 59999))
	httpServer := &http.Server{Addr: config.String("HTTP_ADDR", ":8080"), Handler: api.Handler(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		logger.Info("HTTP API listening", "address", httpServer.Addr)
		if e := httpServer.ListenAndServe(); e != nil && !errors.Is(e, http.ErrServerClosed) {
			logger.Error("HTTP server", "error", e)
		}
	}()
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	grpcServer.GracefulStop()
	return httpServer.Shutdown(shutdownCtx)
}
func serverTLS() (*tls.Config, error) {
	certPath, keyPath, caPath := os.Getenv("GRPC_TLS_CERT"), os.Getenv("GRPC_TLS_KEY"), os.Getenv("GRPC_CLIENT_CA")
	if certPath == "" && keyPath == "" {
		return nil, nil
	}
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, err
	}
	cfg := &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS13}
	if caPath != "" {
		raw, err := os.ReadFile(caPath)
		if err != nil {
			return nil, err
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(raw) {
			return nil, fmt.Errorf("invalid client CA")
		}
		cfg.ClientCAs = pool
		cfg.ClientAuth = tls.RequireAndVerifyClientCert
	}
	return cfg, nil
}

package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proxymesh/proxymesh/db/migrations"
	"github.com/proxymesh/proxymesh/internal/cryptox"
	"github.com/proxymesh/proxymesh/internal/model"
	"github.com/proxymesh/proxymesh/internal/provider"
)

func TestPostgresRouteEncryptionAndCAS(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	cipher, err := cryptox.New("integration-test-key")
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	if err = ApplyMigrations(ctx, pool, migrations.All); err != nil {
		pool.Close()
		t.Fatal(err)
	}
	pool.Close()
	db, err := NewPostgres(ctx, url, cipher)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	definition := provider.Definition{ID: "integration-provider", Enabled: true, Weight: 10, Secrets: map[string]string{"password": "provider-secret-value"}, Config: provider.Config{Protocol: "socks5", Host: provider.ValueRule{Default: "gateway.example.net"}, Port: provider.PortRule{Default: 1080}, Username: provider.ValueRule{Default: "user-{{session}}"}, Password: provider.ValueRule{Default: "{{secret.password}}"}, Session: provider.SessionRule{Type: "uuid"}}}
	if err = db.UpsertProvider(ctx, definition); err != nil {
		t.Fatal(err)
	}
	var providerCiphertext []byte
	if err = db.pool.QueryRow(ctx, `SELECT secrets_cipher FROM proxy_providers WHERE id=$1`, definition.ID).Scan(&providerCiphertext); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(providerCiphertext, []byte(definition.Secrets["password"])) {
		t.Fatal("provider secret was stored as plaintext")
	}
	loadedProvider, err := db.GetProvider(ctx, definition.ID)
	if err != nil || loadedProvider.Secrets["password"] != definition.Secrets["password"] {
		t.Fatalf("provider round trip failed: provider=%#v err=%v", loadedProvider, err)
	}

	now := time.Now().UTC()
	deviceID := fmt.Sprintf("integration-%d", now.UnixNano())
	ingressPassword := "device-shadowsocks-secret"
	ingressPort := 40000 + int(now.UnixNano()%9000)
	if err = db.CreateDevice(ctx, model.Device{ID: deviceID, Username: deviceID, IngressPort: ingressPort, IngressMethod: "aes-256-gcm", IngressPassword: ingressPassword, Enabled: true, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err = db.CreateDevice(ctx, model.Device{ID: deviceID, Username: deviceID, IngressPort: ingressPort + 20, IngressMethod: "aes-256-gcm", IngressPassword: ingressPassword, Enabled: true, CreatedAt: now, UpdatedAt: now}); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected duplicate device id conflict, got %v", err)
	}
	if err = db.CreateDevice(ctx, model.Device{ID: deviceID + "-same-port", Username: deviceID + "-same-port", IngressPort: ingressPort, IngressMethod: "aes-256-gcm", IngressPassword: ingressPassword, Enabled: true, CreatedAt: now, UpdatedAt: now}); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected duplicate ingress port conflict, got %v", err)
	}
	var ingressCiphertext []byte
	if err = db.pool.QueryRow(ctx, `SELECT ingress_password_cipher FROM devices WHERE id=$1`, deviceID).Scan(&ingressCiphertext); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ingressCiphertext, []byte(ingressPassword)) {
		t.Fatal("Shadowsocks password was stored as plaintext")
	}
	ss2022Ingress := model.DeviceIngress{Port: ingressPort + 1, Method: "2022-blake3-aes-128-gcm", Password: "MDEyMzQ1Njc4OWFiY2RlZg==", CreatedAt: now.Add(time.Second)}
	deviceWithMigration, err := db.AddDeviceIngress(ctx, deviceID, ss2022Ingress)
	if err != nil {
		t.Fatal(err)
	}
	if len(deviceWithMigration.Ingresses) != 2 || deviceWithMigration.Ingresses[1].Password != ss2022Ingress.Password {
		t.Fatalf("device ingress migration was not preserved: %#v", deviceWithMigration.Ingresses)
	}
	var ss2022Ciphertext []byte
	if err = db.pool.QueryRow(ctx, `SELECT password_cipher FROM device_ingresses WHERE device_id=$1 AND port=$2`, deviceID, ss2022Ingress.Port).Scan(&ss2022Ciphertext); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ss2022Ciphertext, []byte(ss2022Ingress.Password)) {
		t.Fatal("SS2022 password was stored as plaintext")
	}
	route := model.DeviceRoute{DeviceID: deviceID, DeviceUsername: deviceID, IngressPort: ingressPort, IngressMethod: "aes-256-gcm", IngressPassword: ingressPassword, Version: 1, UpdatedAt: now, Credential: model.ProxyCredential{ID: "proxy-" + deviceID, Host: "127.0.0.1", Port: 1080, Username: "upstream", Password: "never-store-plaintext", CreatedAt: now, ProviderID: definition.ID, GenerationMetadata: map[string]string{"country": "us", "state": "california"}}}
	if err = db.StageRoute(ctx, route, 0); err != nil {
		t.Fatal(err)
	}
	var ciphertext []byte
	if err = db.pool.QueryRow(ctx, `SELECT password_cipher FROM proxy_credentials WHERE id=$1`, route.Credential.ID).Scan(&ciphertext); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ciphertext, []byte(route.Credential.Password)) {
		t.Fatal("SOCKS5 password was stored as plaintext")
	}
	if err = db.ActivateRoute(ctx, deviceID, 1); err != nil {
		t.Fatal(err)
	}
	loaded, err := db.GetRoute(ctx, deviceID)
	if err != nil || loaded.Credential.Password != route.Credential.Password {
		t.Fatalf("route round trip failed: route=%#v err=%v", loaded, err)
	}
	if loaded.IngressPassword != ingressPassword || loaded.IngressPort != ingressPort {
		t.Fatalf("Shadowsocks ingress round trip failed: port=%d", loaded.IngressPort)
	}
	if len(loaded.Ingresses) != 2 || loaded.Ingresses[1].Port != ss2022Ingress.Port {
		t.Fatalf("active route did not include both device ingresses: %#v", loaded.Ingresses)
	}
	if loaded.Credential.ProviderID != definition.ID || loaded.Credential.GenerationMetadata["state"] != "california" {
		t.Fatalf("provider route metadata was not preserved: %#v", loaded.Credential)
	}
	route.Version = 2
	route.Credential.ID += "-conflict"
	if err = db.StageRoute(ctx, route, 0); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected CAS conflict, got %v", err)
	}
	promoted, err := db.DeleteDeviceIngress(ctx, deviceID, ingressPort)
	if err != nil {
		t.Fatal(err)
	}
	if len(promoted.Ingresses) != 1 || !promoted.Ingresses[0].Primary || promoted.IngressPort != ss2022Ingress.Port {
		t.Fatalf("SS2022 ingress was not promoted: %#v", promoted)
	}
}

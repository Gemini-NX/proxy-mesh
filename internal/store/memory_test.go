package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/proxymesh/proxymesh/internal/model"
)

func TestMemoryRouteCAS(t *testing.T) {
	ctx := context.Background()
	s := NewMemory()
	now := time.Now()
	d := model.Device{ID: "d1", Username: "d1", PasswordHash: "hash", Enabled: true, CreatedAt: now, UpdatedAt: now}
	if err := s.CreateDevice(ctx, d); err != nil {
		t.Fatal(err)
	}
	r := model.DeviceRoute{DeviceID: "d1", DeviceUsername: "d1", Version: 1, Credential: model.ProxyCredential{ID: "p1"}}
	if err := s.StageRoute(ctx, r, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.ActivateRoute(ctx, "d1", 1); err != nil {
		t.Fatal(err)
	}
	r.Version = 2
	r.Credential.ID = "p2"
	if err := s.StageRoute(ctx, r, 0); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected conflict, got %v", err)
	}
	if err := s.StageRoute(ctx, r, 1); err != nil {
		t.Fatal(err)
	}
}

func TestMemoryDeviceIngressMigration(t *testing.T) {
	ctx := context.Background()
	s := NewMemory()
	now := time.Now().UTC()
	d := model.Device{ID: "device-1", Username: "device-1", IngressPort: 50000, IngressMethod: "aes-256-gcm", IngressPassword: "legacy-password", Enabled: true, CreatedAt: now, UpdatedAt: now}
	if err := s.CreateDevice(ctx, d); err != nil {
		t.Fatal(err)
	}
	ss2022 := model.DeviceIngress{Port: 50001, Method: "2022-blake3-aes-128-gcm", Password: "MDEyMzQ1Njc4OWFiY2RlZg==", CreatedAt: now.Add(time.Second)}
	updated, err := s.AddDeviceIngress(ctx, d.ID, ss2022)
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Ingresses) != 2 || !updated.Ingresses[0].Primary {
		t.Fatalf("unexpected ingresses: %#v", updated.Ingresses)
	}
	promoted, err := s.DeleteDeviceIngress(ctx, d.ID, 50000)
	if err != nil {
		t.Fatal(err)
	}
	if len(promoted.Ingresses) != 1 || !promoted.Ingresses[0].Primary || promoted.IngressPort != 50001 {
		t.Fatalf("SS2022 ingress was not promoted: %#v", promoted)
	}
	if _, err = s.DeleteDeviceIngress(ctx, d.ID, 50001); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected last-ingress conflict, got %v", err)
	}
}

package control

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/proxymesh/proxymesh/internal/store"
)

func TestCreateDeviceReturnsOneTimeSingBoxCredential(t *testing.T) {
	s := store.NewMemory()
	api := NewAPI(s, NewHub(s, time.Second), "admin", "proxy.example.com", slog.New(slog.NewTextHandler(io.Discard, nil)))
	body := bytes.NewBufferString(`{"id":"device-001"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/devices", body)
	req.Header.Set("Authorization", "Bearer admin")
	rec := httptest.NewRecorder()
	api.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response["password"] == "" {
		t.Fatal("password missing")
	}
	outbound := response["singBoxOutbound"].(map[string]any)
	if outbound["type"] != "shadowsocks" || outbound["method"] != "aes-256-gcm" || outbound["server"] != "proxy.example.com" {
		t.Fatalf("unexpected outbound: %#v", outbound)
	}
	device, err := s.GetDevice(req.Context(), "device-001")
	if err != nil {
		t.Fatal(err)
	}
	if device.IngressPort == 0 || device.IngressPassword == "" {
		t.Fatal("Shadowsocks ingress was not stored")
	}
}

func TestCreateDeviceWithShadowsocks2022(t *testing.T) {
	s := store.NewMemory()
	api := NewAPI(s, NewHub(s, time.Second), "admin", "proxy.example.com", slog.New(slog.NewTextHandler(io.Discard, nil)))
	body := bytes.NewBufferString(`{"id":"device-2022","shadowsocksMethod":"2022-blake3-aes-128-gcm"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/devices", body)
	req.Header.Set("Authorization", "Bearer admin")
	rec := httptest.NewRecorder()
	api.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	key, err := base64.StdEncoding.DecodeString(response["password"].(string))
	if err != nil || len(key) != 16 {
		t.Fatalf("invalid generated SS2022 key: bytes=%d err=%v", len(key), err)
	}
	if response["method"] != "2022-blake3-aes-128-gcm" {
		t.Fatalf("unexpected method: %v", response["method"])
	}
}
func TestAdminTokenRequired(t *testing.T) {
	s := store.NewMemory()
	api := NewAPI(s, NewHub(s, time.Second), "admin", "proxy.example.com", slog.Default())
	req := httptest.NewRequest(http.MethodGet, "/v1/gateways", nil)
	rec := httptest.NewRecorder()
	api.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got %d", rec.Code)
	}
}

func TestProviderSecretsAreWriteOnly(t *testing.T) {
	s := store.NewMemory()
	api := NewAPI(s, NewHub(s, time.Second), "admin", "proxy.example.com", slog.Default())
	body := bytes.NewBufferString(`{"enabled":true,"weight":50,"config":{"protocol":"socks5","host":{"default":"gateway.example.net"},"port":{"default":1080},"username":{"default":"account-{{session}}"},"password":{"default":"{{secret.password}}"},"session":{"type":"uuid"}},"secrets":{"password":"must-not-be-returned"}}`)
	req := httptest.NewRequest(http.MethodPut, "/v1/providers/vendor-a", body)
	req.Header.Set("Authorization", "Bearer admin")
	rec := httptest.NewRecorder()
	api.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if bytes.Contains(rec.Body.Bytes(), []byte("must-not-be-returned")) {
		t.Fatal("provider secret was returned by API")
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"secretKeys":["password"]`)) {
		t.Fatalf("secret key metadata missing: %s", rec.Body.String())
	}

	stored, err := s.GetProvider(req.Context(), "vendor-a")
	if err != nil || stored.Secrets["password"] != "must-not-be-returned" {
		t.Fatalf("provider was not stored: provider=%#v err=%v", stored, err)
	}
}

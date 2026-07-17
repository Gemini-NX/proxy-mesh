package control

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/proxymesh/proxymesh/internal/model"
	"github.com/proxymesh/proxymesh/internal/provider"
	"github.com/proxymesh/proxymesh/internal/ssconfig"
	"github.com/proxymesh/proxymesh/internal/store"
)

type API struct {
	store                  store.Store
	hub                    *Hub
	adminToken, publicHost string
	ingressPortStart       int
	ingressPortEnd         int
	logger                 *slog.Logger
}

func NewAPI(s store.Store, h *Hub, adminToken, publicHost string, l *slog.Logger) *API {
	return &API{store: s, hub: h, adminToken: adminToken, publicHost: publicHost, ingressPortStart: 50000, ingressPortEnd: 59999, logger: l}
}
func (a *API) SetIngressPortRange(start, end int) {
	if start >= 1024 && end >= start && end <= 65535 {
		a.ingressPortStart, a.ingressPortEnd = start, end
	}
}
func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /live", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("POST /v1/devices", a.createDevice)
	mux.HandleFunc("POST /v1/devices/{deviceId}/credentials/rotate", a.rotateCredential)
	mux.HandleFunc("POST /v1/devices/{deviceId}/ingresses", a.addIngress)
	mux.HandleFunc("DELETE /v1/devices/{deviceId}/ingresses/{port}", a.deleteIngress)
	mux.HandleFunc("PUT /v1/devices/{deviceId}/route", a.putRoute)
	mux.HandleFunc("GET /v1/devices/{deviceId}/status", a.deviceStatus)
	mux.HandleFunc("POST /v1/devices/{deviceId}/connections/interrupt", a.interrupt)
	mux.HandleFunc("GET /v1/gateways", a.gateways)
	mux.HandleFunc("POST /v1/gateways/{gatewayId}/draining", a.setGatewayDraining)
	mux.HandleFunc("PUT /v1/providers/{providerId}", a.putProvider)
	mux.HandleFunc("GET /v1/providers", a.providers)
	mux.HandleFunc("POST /v1/devices/{deviceId}/route/from-provider", a.putRouteFromProvider)
	return a.auth(mux)
}
func (a *API) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/live" {
			next.ServeHTTP(w, r)
			return
		}
		provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if a.adminToken == "" || subtle.ConstantTimeCompare([]byte(provided), []byte(a.adminToken)) != 1 {
			writeError(w, 401, "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}

type createDeviceRequest struct {
	ID                  string `json:"id"`
	ShadowsocksMethod   string `json:"shadowsocksMethod,omitempty"`
	ShadowsocksPassword string `json:"shadowsocksPassword,omitempty"`
	ListenPort          int    `json:"listenPort,omitempty"`
}

var validDeviceID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

func (a *API) createDevice(w http.ResponseWriter, r *http.Request) {
	var req createDeviceRequest
	if err := decode(r, &req); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if req.ID == "" {
		req.ID = "device-" + randomToken(8)
	}
	if !validDeviceID.MatchString(req.ID) {
		writeError(w, 400, "device id must be 1-64 letters, digits, dots, underscores or hyphens")
		return
	}
	method := req.ShadowsocksMethod
	if method == "" {
		method = ssconfig.LegacyAES256GCM
	}
	password := req.ShadowsocksPassword
	if password == "" {
		var err error
		password, err = ssconfig.GeneratePassword(method)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
	}
	if err := ssconfig.Validate(method, password); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if req.ListenPort != 0 && (req.ListenPort < 1024 || req.ListenPort > 65535) {
		writeError(w, 400, "listenPort must be between 1024 and 65535")
		return
	}
	now := time.Now().UTC()
	d := model.Device{ID: req.ID, Username: req.ID, IngressMethod: method, IngressPassword: password, Enabled: true, CreatedAt: now, UpdatedAt: now}
	var err error
	if req.ListenPort != 0 {
		d.IngressPort = req.ListenPort
		err = a.store.CreateDevice(r.Context(), d)
	} else {
		for attempt := 0; attempt < 64; attempt++ {
			d.IngressPort = a.ingressPortStart + randomInt(a.ingressPortEnd-a.ingressPortStart+1)
			err = a.store.CreateDevice(r.Context(), d)
			if err == nil || !errors.Is(err, store.ErrConflict) {
				break
			}
		}
	}
	if err != nil {
		writeStoreError(w, err)
		return
	}
	_ = a.store.AppendAudit(r.Context(), "api", "device.create", d.ID, map[string]any{})
	writeJSON(w, 201, map[string]any{"deviceId": d.ID, "listenPort": d.IngressPort, "method": method, "password": password, "singBoxOutbound": map[string]any{"type": "shadowsocks", "tag": "ss-out", "server": a.publicHost, "server_port": d.IngressPort, "method": method, "password": password, "multiplex": map[string]any{"enabled": false, "padding": true}}})
}
func (a *API) rotateCredential(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("deviceId")
	current, err := a.store.GetDevice(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	password, err := ssconfig.GeneratePassword(current.IngressMethod)
	if err != nil {
		writeError(w, 500, "generate Shadowsocks credential failed")
		return
	}
	proposed := current
	proposed.IngressPassword = password
	for i := range proposed.Ingresses {
		if proposed.Ingresses[i].Primary {
			proposed.Ingresses[i].Password = password
		}
	}
	var d model.Device
	if err = a.hub.DeployDevice(r.Context(), proposed, func() error {
		var updateErr error
		d, updateErr = a.store.UpdateDeviceCredential(r.Context(), id, password)
		return updateErr
	}); err != nil {
		writeError(w, 503, err.Error())
		return
	}
	_ = a.store.AppendAudit(r.Context(), "api", "device.credential.rotate", id, map[string]any{})
	writeJSON(w, 200, map[string]any{"deviceId": id, "listenPort": d.IngressPort, "method": d.IngressMethod, "password": password})
}

type addIngressRequest struct {
	Method     string `json:"method,omitempty"`
	Password   string `json:"password,omitempty"`
	ListenPort int    `json:"listenPort,omitempty"`
}

func (a *API) addIngress(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("deviceId")
	current, err := a.store.GetDevice(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	var req addIngressRequest
	if err = decode(r, &req); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if req.Method == "" {
		req.Method = ssconfig.Blake3AES128GCM
	}
	if req.Password == "" {
		req.Password, err = ssconfig.GeneratePassword(req.Method)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
	}
	if err = ssconfig.Validate(req.Method, req.Password); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if req.ListenPort == 0 {
		req.ListenPort = a.ingressPortStart + randomInt(a.ingressPortEnd-a.ingressPortStart+1)
	}
	if req.ListenPort < 1024 || req.ListenPort > 65535 {
		writeError(w, 400, "listenPort must be between 1024 and 65535")
		return
	}
	for _, existing := range current.EffectiveIngresses() {
		if existing.Port == req.ListenPort {
			writeError(w, 409, "ingress port already belongs to this device")
			return
		}
	}
	ingress := model.DeviceIngress{Port: req.ListenPort, Method: req.Method, Password: req.Password, CreatedAt: time.Now().UTC()}
	proposed := current
	proposed.Ingresses = append(append([]model.DeviceIngress(nil), current.EffectiveIngresses()...), ingress)
	var updated model.Device
	if err = a.hub.DeployDevice(r.Context(), proposed, func() error {
		var addErr error
		updated, addErr = a.store.AddDeviceIngress(r.Context(), id, ingress)
		return addErr
	}); err != nil {
		writeError(w, 503, err.Error())
		return
	}
	_ = a.store.AppendAudit(r.Context(), "api", "device.ingress.add", id, map[string]any{"port": ingress.Port, "method": ingress.Method})
	writeJSON(w, 201, map[string]any{"deviceId": id, "listenPort": ingress.Port, "method": ingress.Method, "password": ingress.Password, "primary": false, "singBoxOutbound": singBoxOutbound(a.publicHost, ingress), "ingresses": sanitizeIngresses(updated)})
}

func (a *API) deleteIngress(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("deviceId")
	port, err := parsePort(r.PathValue("port"))
	if err != nil {
		writeError(w, 400, "invalid ingress port")
		return
	}
	current, err := a.store.GetDevice(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	ingresses := current.EffectiveIngresses()
	if len(ingresses) <= 1 {
		writeError(w, 409, "cannot delete the last device ingress")
		return
	}
	proposed := current
	proposed.Ingresses = make([]model.DeviceIngress, 0, len(ingresses)-1)
	removed, removedPrimary := false, false
	for _, ingress := range ingresses {
		if ingress.Port == port {
			removed, removedPrimary = true, ingress.Primary
			continue
		}
		proposed.Ingresses = append(proposed.Ingresses, ingress)
	}
	if !removed {
		writeError(w, 404, "ingress not found")
		return
	}
	if removedPrimary {
		proposed.Ingresses[0].Primary = true
		primary := proposed.Ingresses[0]
		proposed.IngressPort, proposed.IngressMethod, proposed.IngressPassword = primary.Port, primary.Method, primary.Password
	}
	if err = a.hub.DeployDevice(r.Context(), proposed, func() error {
		_, deleteErr := a.store.DeleteDeviceIngress(r.Context(), id, port)
		return deleteErr
	}); err != nil {
		writeError(w, 503, err.Error())
		return
	}
	_ = a.store.AppendAudit(r.Context(), "api", "device.ingress.delete", id, map[string]any{"port": port})
	w.WriteHeader(http.StatusNoContent)
}

type putRouteRequest struct {
	Host            string     `json:"host"`
	Port            int        `json:"port"`
	Username        string     `json:"username"`
	Password        string     `json:"password"`
	ExpiresAt       *time.Time `json:"expiresAt"`
	ExpectedVersion int64      `json:"expectedVersion"`
}

func (a *API) putRoute(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("deviceId")
	var req putRouteRequest
	if err := decode(r, &req); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if req.Host == "" || req.Port < 1 || req.Port > 65535 {
		writeError(w, 400, "valid host and port are required")
		return
	}
	if req.ExpectedVersion < 0 {
		writeError(w, 400, "expectedVersion must not be negative")
		return
	}
	d, err := a.store.GetDevice(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	now := time.Now().UTC()
	route := model.DeviceRoute{DeviceID: id, DeviceUsername: d.Username, IngressPort: d.IngressPort, IngressMethod: d.IngressMethod, IngressPassword: d.IngressPassword, Ingresses: d.Ingresses, Version: req.ExpectedVersion + 1, Status: model.RoutePending, UpdatedAt: now, Credential: model.ProxyCredential{ID: "proxy-" + randomToken(12), Host: req.Host, Port: req.Port, Username: req.Username, Password: req.Password, ExpiresAt: req.ExpiresAt, CreatedAt: now}}
	a.deployRoute(w, r, route, req.ExpectedVersion)
}

type putProviderRequest struct {
	Enabled bool              `json:"enabled"`
	Weight  int               `json:"weight"`
	Config  provider.Config   `json:"config"`
	Secrets map[string]string `json:"secrets,omitempty"`
}

func (a *API) putProvider(w http.ResponseWriter, r *http.Request) {
	var req putProviderRequest
	if err := decode(r, &req); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	d := provider.Definition{ID: r.PathValue("providerId"), Enabled: req.Enabled, Weight: req.Weight, Config: req.Config, Secrets: req.Secrets}
	if len(d.Secrets) == 0 {
		if current, err := a.store.GetProvider(r.Context(), d.ID); err == nil {
			d.Secrets = current.Secrets
		}
	}
	if err := provider.Validate(d); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if err := a.store.UpsertProvider(r.Context(), d); err != nil {
		writeStoreError(w, err)
		return
	}
	_ = a.store.AppendAudit(r.Context(), "api", "provider.upsert", d.ID, map[string]any{"enabled": d.Enabled, "weight": d.Weight})
	writeJSON(w, 200, sanitizeProvider(d))
}

func (a *API) providers(w http.ResponseWriter, r *http.Request) {
	items, err := a.store.ListProviders(r.Context())
	if err != nil {
		writeError(w, 500, "list providers failed")
		return
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, sanitizeProvider(item))
	}
	writeJSON(w, 200, out)
}

type putRouteFromProviderRequest struct {
	ProviderID      string `json:"providerId,omitempty"`
	Country         string `json:"country"`
	State           string `json:"state,omitempty"`
	City            string `json:"city,omitempty"`
	ExpectedVersion int64  `json:"expectedVersion"`
}

func (a *API) putRouteFromProvider(w http.ResponseWriter, r *http.Request) {
	var req putRouteFromProviderRequest
	if err := decode(r, &req); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	var definition provider.Definition
	var err error
	if req.ProviderID != "" {
		definition, err = a.store.GetProvider(r.Context(), req.ProviderID)
	} else {
		definitions, listErr := a.store.ListProviders(r.Context())
		if listErr != nil {
			writeError(w, 500, "list providers failed")
			return
		}
		definition, err = provider.SelectWeighted(definitions)
		if err != nil {
			writeError(w, 409, err.Error())
			return
		}
	}
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if req.ExpectedVersion < 0 {
		writeError(w, 400, "expectedVersion must not be negative")
		return
	}
	if !definition.Enabled {
		writeError(w, 409, "proxy provider is disabled")
		return
	}
	endpoint, err := provider.Generate(definition, provider.Request{Country: req.Country, State: req.State, City: req.City})
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	d, err := a.store.GetDevice(r.Context(), r.PathValue("deviceId"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	now := time.Now().UTC()
	var expiresAt *time.Time
	if definition.Config.RouteTTLMinutes > 0 {
		value := now.Add(time.Duration(definition.Config.RouteTTLMinutes) * time.Minute)
		expiresAt = &value
	}
	route := model.DeviceRoute{DeviceID: d.ID, DeviceUsername: d.Username, IngressPort: d.IngressPort, IngressMethod: d.IngressMethod, IngressPassword: d.IngressPassword, Ingresses: d.Ingresses, Version: req.ExpectedVersion + 1, Status: model.RoutePending, UpdatedAt: now, Credential: model.ProxyCredential{ID: "proxy-" + randomToken(12), Host: endpoint.Host, Port: endpoint.Port, Username: endpoint.Username, Password: endpoint.Password, ExpiresAt: expiresAt, CreatedAt: now, ProviderID: definition.ID, GenerationMetadata: map[string]string{"country": endpoint.Country, "state": endpoint.State, "city": endpoint.City}}}
	a.deployRoute(w, r, route, req.ExpectedVersion)
}

func (a *API) deployRoute(w http.ResponseWriter, r *http.Request, route model.DeviceRoute, expectedVersion int64) {
	if err := a.store.StageRoute(r.Context(), route, expectedVersion); err != nil {
		writeStoreError(w, err)
		return
	}
	if err := a.hub.DeployRoute(r.Context(), route, func() error {
		return a.store.ActivateRoute(r.Context(), route.DeviceID, route.Version)
	}); err != nil {
		writeError(w, 503, err.Error())
		return
	}
	route.Status = model.RouteActive
	_ = a.store.AppendAudit(r.Context(), "api", "route.activate", route.DeviceID, map[string]any{"version": route.Version, "providerId": route.Credential.ProviderID, "proxyHost": route.Credential.Host, "proxyPort": route.Credential.Port})
	writeJSON(w, 200, sanitizeRoute(route))
}
func (a *API) deviceStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("deviceId")
	d, err := a.store.GetDevice(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	route, err := a.store.GetRoute(r.Context(), id)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		writeStoreError(w, err)
		return
	}
	response := map[string]any{"deviceId": d.ID, "username": d.Username, "enabled": d.Enabled, "ingresses": sanitizeIngresses(d)}
	if err == nil {
		response["route"] = sanitizeRoute(route)
	}
	writeJSON(w, 200, response)
}
func (a *API) interrupt(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("deviceId")
	if _, err := a.store.GetDevice(r.Context(), id); err != nil {
		writeStoreError(w, err)
		return
	}
	if err := a.hub.Interrupt(r.Context(), id); err != nil {
		writeError(w, 503, err.Error())
		return
	}
	_ = a.store.AppendAudit(r.Context(), "api", "connections.interrupt", id, map[string]any{})
	w.WriteHeader(http.StatusNoContent)
}
func (a *API) gateways(w http.ResponseWriter, r *http.Request) {
	gs, err := a.store.ListGateways(r.Context())
	if err != nil {
		writeError(w, 500, "list gateways failed")
		return
	}
	writeJSON(w, 200, gs)
}
func (a *API) setGatewayDraining(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Draining bool `json:"draining"`
	}
	if err := decode(r, &req); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	id := r.PathValue("gatewayId")
	if err := a.hub.SetDraining(r.Context(), id, req.Draining); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, 404, "gateway is not connected")
		} else {
			writeError(w, 503, err.Error())
		}
		return
	}
	_ = a.store.AppendAudit(r.Context(), "api", "gateway.draining.set", id, map[string]any{"draining": req.Draining})
	w.WriteHeader(http.StatusNoContent)
}
func sanitizeRoute(r model.DeviceRoute) map[string]any {
	return map[string]any{"deviceId": r.DeviceID, "version": r.Version, "status": r.Status, "providerId": r.Credential.ProviderID, "host": r.Credential.Host, "port": r.Credential.Port, "username": r.Credential.Username, "location": r.Credential.GenerationMetadata, "expiresAt": r.Credential.ExpiresAt, "updatedAt": r.UpdatedAt}
}
func sanitizeProvider(d provider.Definition) map[string]any {
	keys := make([]string, 0, len(d.Secrets))
	for key := range d.Secrets {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return map[string]any{"id": d.ID, "enabled": d.Enabled, "weight": d.Weight, "config": d.Config, "secretKeys": keys}
}
func sanitizeIngresses(d model.Device) []map[string]any {
	ingresses := d.EffectiveIngresses()
	out := make([]map[string]any, 0, len(ingresses))
	for _, ingress := range ingresses {
		out = append(out, map[string]any{"listenPort": ingress.Port, "method": ingress.Method, "primary": ingress.Primary, "createdAt": ingress.CreatedAt})
	}
	return out
}
func singBoxOutbound(host string, ingress model.DeviceIngress) map[string]any {
	return map[string]any{"type": "shadowsocks", "tag": "ss-out", "server": host, "server_port": ingress.Port, "method": ingress.Method, "password": ingress.Password, "multiplex": map[string]any{"enabled": false, "padding": true}}
}
func parsePort(value string) (int, error) {
	port, err := strconv.Atoi(value)
	if err != nil || port < 1 || port > 65535 {
		return 0, errors.New("invalid port")
	}
	return port, nil
}
func decode(r *http.Request, v any) error {
	defer r.Body.Close()
	d := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	d.DisallowUnknownFields()
	return d.Decode(v)
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
func writeStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, 404, "not found")
	case errors.Is(err, store.ErrConflict):
		writeError(w, 409, "version conflict")
	default:
		writeError(w, 500, "storage operation failed")
	}
}
func randomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func randomInt(max int) int {
	v, err := rand.Int(rand.Reader, big.NewInt(int64(max)))
	if err != nil {
		panic(err)
	}
	return int(v.Int64())
}

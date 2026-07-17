package model

import "time"

type DeviceIngress struct {
	Port      int       `json:"port"`
	Method    string    `json:"method"`
	Password  string    `json:"password,omitempty"`
	Primary   bool      `json:"primary"`
	CreatedAt time.Time `json:"createdAt"`
}

type Device struct {
	ID              string          `json:"id"`
	Username        string          `json:"username"`
	PasswordHash    string          `json:"passwordHash,omitempty"` // Retained only for migration from HTTPS CONNECT.
	IngressPort     int             `json:"ingressPort"`
	IngressMethod   string          `json:"ingressMethod"`
	IngressPassword string          `json:"ingressPassword,omitempty"`
	Ingresses       []DeviceIngress `json:"ingresses,omitempty"`
	Enabled         bool            `json:"enabled"`
	CreatedAt       time.Time       `json:"createdAt"`
	UpdatedAt       time.Time       `json:"updatedAt"`
}

type ProxyCredential struct {
	ID                 string            `json:"id"`
	Host               string            `json:"host"`
	Port               int               `json:"port"`
	Username           string            `json:"username,omitempty"`
	Password           string            `json:"password,omitempty"`
	EncryptedPassword  []byte            `json:"-"`
	ExpiresAt          *time.Time        `json:"expiresAt,omitempty"`
	CreatedAt          time.Time         `json:"createdAt"`
	ProviderID         string            `json:"providerId,omitempty"`
	GenerationMetadata map[string]string `json:"generationMetadata,omitempty"`
}

type DeviceRoute struct {
	DeviceID        string          `json:"deviceId"`
	DeviceUsername  string          `json:"deviceUsername"`
	PasswordHash    string          `json:"passwordHash"`
	IngressPort     int             `json:"ingressPort"`
	IngressMethod   string          `json:"ingressMethod"`
	IngressPassword string          `json:"ingressPassword"`
	Ingresses       []DeviceIngress `json:"ingresses,omitempty"`
	Credential      ProxyCredential `json:"credential"`
	Version         int64           `json:"version"`
	Status          string          `json:"status"`
	UpdatedAt       time.Time       `json:"updatedAt"`
}

func (d Device) EffectiveIngresses() []DeviceIngress {
	if len(d.Ingresses) > 0 {
		return d.Ingresses
	}
	if d.IngressPort == 0 {
		return nil
	}
	return []DeviceIngress{{Port: d.IngressPort, Method: d.IngressMethod, Password: d.IngressPassword, Primary: true, CreatedAt: d.CreatedAt}}
}

func (r DeviceRoute) EffectiveIngresses() []DeviceIngress {
	if len(r.Ingresses) > 0 {
		return r.Ingresses
	}
	if r.IngressPort == 0 {
		return nil
	}
	return []DeviceIngress{{Port: r.IngressPort, Method: r.IngressMethod, Password: r.IngressPassword, Primary: true, CreatedAt: r.UpdatedAt}}
}

type Gateway struct {
	ID                string    `json:"id"`
	Address           string    `json:"address,omitempty"`
	Status            string    `json:"status"`
	AppliedVersion    int64     `json:"appliedVersion"`
	ActiveConnections int64     `json:"activeConnections"`
	LastHeartbeatAt   time.Time `json:"lastHeartbeatAt"`
}

const (
	RoutePending    = "pending"
	RouteActive     = "active"
	GatewayReady    = "READY"
	GatewaySyncing  = "SYNCING"
	GatewayDraining = "DRAINING"
	GatewayOffline  = "OFFLINE"
)

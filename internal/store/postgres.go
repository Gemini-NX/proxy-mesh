package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proxymesh/proxymesh/internal/cryptox"
	"github.com/proxymesh/proxymesh/internal/model"
	"github.com/proxymesh/proxymesh/internal/provider"
)

type Postgres struct {
	pool   *pgxpool.Pool
	cipher *cryptox.Cipher
}

func NewPostgres(ctx context.Context, url string, c *cryptox.Cipher) (*Postgres, error) {
	p, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, err
	}
	if err = p.Ping(ctx); err != nil {
		p.Close()
		return nil, err
	}
	return &Postgres{pool: p, cipher: c}, nil
}
func (p *Postgres) Close() { p.pool.Close() }

func (p *Postgres) CreateDevice(ctx context.Context, d model.Device) error {
	var enc any
	if d.IngressPassword != "" {
		ciphertext, err := p.cipher.Encrypt([]byte(d.IngressPassword))
		if err != nil {
			return err
		}
		enc = ciphertext
	}
	var ingressPort any
	if d.IngressPort != 0 {
		ingressPort = d.IngressPort
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `INSERT INTO devices(id,username,password_hash,ingress_port,ingress_method,ingress_password_cipher,enabled,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, d.ID, d.Username, d.PasswordHash, ingressPort, d.IngressMethod, enc, d.Enabled, d.CreatedAt, d.UpdatedAt); err != nil {
		return mapErr(err)
	}
	for _, ingress := range d.EffectiveIngresses() {
		passwordCipher, encryptErr := p.cipher.Encrypt([]byte(ingress.Password))
		if encryptErr != nil {
			return encryptErr
		}
		if _, err = tx.Exec(ctx, `INSERT INTO device_ingresses(device_id,port,method,password_cipher,is_primary,created_at) VALUES($1,$2,$3,$4,$5,$6)`, d.ID, ingress.Port, ingress.Method, passwordCipher, ingress.Primary, ingress.CreatedAt); err != nil {
			return mapErr(err)
		}
	}
	return tx.Commit(ctx)
}
func (p *Postgres) GetDevice(ctx context.Context, id string) (model.Device, error) {
	var d model.Device
	var enc []byte
	err := p.pool.QueryRow(ctx, `SELECT id,username,password_hash,COALESCE(ingress_port,0),ingress_method,COALESCE(ingress_password_cipher,'\x'::bytea),enabled,created_at,updated_at FROM devices WHERE id=$1`, id).Scan(&d.ID, &d.Username, &d.PasswordHash, &d.IngressPort, &d.IngressMethod, &enc, &d.Enabled, &d.CreatedAt, &d.UpdatedAt)
	if err == nil && len(enc) > 0 {
		plain, decryptErr := p.cipher.Decrypt(enc)
		if decryptErr != nil {
			return d, decryptErr
		}
		d.IngressPassword = string(plain)
	}
	if err != nil {
		return d, mapErr(err)
	}
	d.Ingresses, err = p.loadDeviceIngresses(ctx, id)
	return d, err
}
func (p *Postgres) UpdateDeviceCredential(ctx context.Context, id, password string) (model.Device, error) {
	var d model.Device
	enc, err := p.cipher.Encrypt([]byte(password))
	if err != nil {
		return d, err
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return d, err
	}
	defer tx.Rollback(ctx)
	err = tx.QueryRow(ctx, `UPDATE devices SET ingress_password_cipher=$2,updated_at=now() WHERE id=$1 RETURNING id,username,password_hash,COALESCE(ingress_port,0),ingress_method,enabled,created_at,updated_at`, id, enc).Scan(&d.ID, &d.Username, &d.PasswordHash, &d.IngressPort, &d.IngressMethod, &d.Enabled, &d.CreatedAt, &d.UpdatedAt)
	if err != nil {
		return d, mapErr(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE device_ingresses SET password_cipher=$2 WHERE device_id=$1 AND is_primary`, id, enc); err != nil {
		return d, err
	}
	if err = tx.Commit(ctx); err != nil {
		return d, err
	}
	d.IngressPassword = password
	d.Ingresses, err = p.loadDeviceIngresses(ctx, id)
	return d, err
}

func (p *Postgres) AddDeviceIngress(ctx context.Context, id string, ingress model.DeviceIngress) (model.Device, error) {
	passwordCipher, err := p.cipher.Encrypt([]byte(ingress.Password))
	if err != nil {
		return model.Device{}, err
	}
	cmd, err := p.pool.Exec(ctx, `INSERT INTO device_ingresses(device_id,port,method,password_cipher,is_primary,created_at) VALUES($1,$2,$3,$4,false,$5)`, id, ingress.Port, ingress.Method, passwordCipher, ingress.CreatedAt)
	if err != nil {
		return model.Device{}, mapErr(err)
	}
	if cmd.RowsAffected() != 1 {
		return model.Device{}, ErrConflict
	}
	if _, err = p.pool.Exec(ctx, `UPDATE devices SET updated_at=now() WHERE id=$1`, id); err != nil {
		return model.Device{}, err
	}
	return p.GetDevice(ctx, id)
}

func (p *Postgres) DeleteDeviceIngress(ctx context.Context, id string, port int) (model.Device, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return model.Device{}, err
	}
	defer tx.Rollback(ctx)
	var lockedID string
	if err = tx.QueryRow(ctx, `SELECT id FROM devices WHERE id=$1 FOR UPDATE`, id).Scan(&lockedID); err != nil {
		return model.Device{}, mapErr(err)
	}
	var count int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM device_ingresses WHERE device_id=$1`, id).Scan(&count); err != nil {
		return model.Device{}, mapErr(err)
	}
	if count <= 1 {
		return model.Device{}, ErrConflict
	}
	var primary bool
	if err = tx.QueryRow(ctx, `DELETE FROM device_ingresses WHERE device_id=$1 AND port=$2 RETURNING is_primary`, id, port).Scan(&primary); err != nil {
		return model.Device{}, mapErr(err)
	}
	if primary {
		var next model.DeviceIngress
		var passwordCipher []byte
		if err = tx.QueryRow(ctx, `SELECT port,method,password_cipher,created_at FROM device_ingresses WHERE device_id=$1 ORDER BY created_at,port LIMIT 1`, id).Scan(&next.Port, &next.Method, &passwordCipher, &next.CreatedAt); err != nil {
			return model.Device{}, mapErr(err)
		}
		if _, err = tx.Exec(ctx, `UPDATE device_ingresses SET is_primary=true WHERE device_id=$1 AND port=$2`, id, next.Port); err != nil {
			return model.Device{}, err
		}
		if _, err = tx.Exec(ctx, `UPDATE devices SET ingress_port=$2,ingress_method=$3,ingress_password_cipher=$4,updated_at=now() WHERE id=$1`, id, next.Port, next.Method, passwordCipher); err != nil {
			return model.Device{}, err
		}
	} else if _, err = tx.Exec(ctx, `UPDATE devices SET updated_at=now() WHERE id=$1`, id); err != nil {
		return model.Device{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return model.Device{}, err
	}
	return p.GetDevice(ctx, id)
}

func (p *Postgres) loadDeviceIngresses(ctx context.Context, id string) ([]model.DeviceIngress, error) {
	rows, err := p.pool.Query(ctx, `SELECT port,method,password_cipher,is_primary,created_at FROM device_ingresses WHERE device_id=$1 ORDER BY is_primary DESC,created_at,port`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.DeviceIngress
	for rows.Next() {
		var ingress model.DeviceIngress
		var passwordCipher []byte
		if err = rows.Scan(&ingress.Port, &ingress.Method, &passwordCipher, &ingress.Primary, &ingress.CreatedAt); err != nil {
			return nil, err
		}
		plain, decryptErr := p.cipher.Decrypt(passwordCipher)
		if decryptErr != nil {
			return nil, decryptErr
		}
		ingress.Password = string(plain)
		out = append(out, ingress)
	}
	return out, rows.Err()
}
func (p *Postgres) UpsertProvider(ctx context.Context, d provider.Definition) error {
	configJSON, err := json.Marshal(d.Config)
	if err != nil {
		return err
	}
	secretJSON, err := json.Marshal(d.Secrets)
	if err != nil {
		return err
	}
	ciphertext, err := p.cipher.Encrypt(secretJSON)
	if err != nil {
		return err
	}
	_, err = p.pool.Exec(ctx, `INSERT INTO proxy_providers(id,enabled,weight,config,secrets_cipher) VALUES($1,$2,$3,$4,$5) ON CONFLICT(id) DO UPDATE SET enabled=excluded.enabled,weight=excluded.weight,config=excluded.config,secrets_cipher=excluded.secrets_cipher,updated_at=now()`, d.ID, d.Enabled, d.Weight, configJSON, ciphertext)
	return err
}
func (p *Postgres) GetProvider(ctx context.Context, id string) (provider.Definition, error) {
	var d provider.Definition
	var configJSON, ciphertext []byte
	err := p.pool.QueryRow(ctx, `SELECT id,enabled,weight,config,secrets_cipher FROM proxy_providers WHERE id=$1`, id).Scan(&d.ID, &d.Enabled, &d.Weight, &configJSON, &ciphertext)
	if err != nil {
		return d, mapErr(err)
	}
	if err = json.Unmarshal(configJSON, &d.Config); err != nil {
		return d, err
	}
	plain, err := p.cipher.Decrypt(ciphertext)
	if err != nil {
		return d, err
	}
	if err = json.Unmarshal(plain, &d.Secrets); err != nil {
		return d, err
	}
	return d, nil
}
func (p *Postgres) ListProviders(ctx context.Context) ([]provider.Definition, error) {
	rows, err := p.pool.Query(ctx, `SELECT id FROM proxy_providers ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	out := make([]provider.Definition, 0, len(ids))
	for _, id := range ids {
		d, err := p.GetProvider(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}
func (p *Postgres) StageRoute(ctx context.Context, r model.DeviceRoute, expected int64) error {
	enc, err := p.cipher.Encrypt([]byte(r.Credential.Password))
	if err != nil {
		return err
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var lockedID string
	if err = tx.QueryRow(ctx, `SELECT id FROM devices WHERE id=$1 FOR UPDATE`, r.DeviceID).Scan(&lockedID); err != nil {
		return mapErr(err)
	}
	var current int64
	err = tx.QueryRow(ctx, `SELECT COALESCE(MAX(version) FILTER (WHERE status='active'),0) FROM device_routes WHERE device_id=$1`, r.DeviceID).Scan(&current)
	if err != nil {
		return err
	}
	if current != expected {
		return ErrConflict
	}
	rows, err := tx.Query(ctx, `DELETE FROM device_routes WHERE device_id=$1 AND status='pending' RETURNING credential_id`, r.DeviceID)
	if err != nil {
		return err
	}
	var staleCredentialIDs []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		staleCredentialIDs = append(staleCredentialIDs, id)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, id := range staleCredentialIDs {
		if _, err = tx.Exec(ctx, `DELETE FROM proxy_credentials WHERE id=$1`, id); err != nil {
			return err
		}
	}
	metadata, err := json.Marshal(r.Credential.GenerationMetadata)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO proxy_credentials(id,host,port,username,password_cipher,expires_at,created_at,provider_id,generation_metadata) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, r.Credential.ID, r.Credential.Host, r.Credential.Port, r.Credential.Username, enc, r.Credential.ExpiresAt, r.Credential.CreatedAt, nullableString(r.Credential.ProviderID), metadata)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO device_routes(device_id,credential_id,version,status,updated_at) VALUES($1,$2,$3,'pending',$4)`, r.DeviceID, r.Credential.ID, r.Version, r.UpdatedAt)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (p *Postgres) ActivateRoute(ctx context.Context, id string, version int64) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	cmd, err := tx.Exec(ctx, `UPDATE device_routes SET status='superseded',updated_at=now() WHERE device_id=$1 AND status='active'`, id)
	_ = cmd
	if err != nil {
		return err
	}
	cmd, err = tx.Exec(ctx, `UPDATE device_routes SET status='active',updated_at=now() WHERE device_id=$1 AND version=$2 AND status='pending'`, id, version)
	if err != nil {
		return err
	}
	if cmd.RowsAffected() != 1 {
		return ErrConflict
	}
	return tx.Commit(ctx)
}
func (p *Postgres) GetRoute(ctx context.Context, id string) (model.DeviceRoute, error) {
	return p.getRoute(ctx, `WHERE r.device_id=$1 AND r.status='active'`, id)
}
func (p *Postgres) getRoute(ctx context.Context, where string, args ...any) (model.DeviceRoute, error) {
	q := `SELECT d.id,d.username,d.password_hash,COALESCE(d.ingress_port,0),d.ingress_method,COALESCE(d.ingress_password_cipher,'\x'::bytea),c.id,c.host,c.port,c.username,c.password_cipher,c.expires_at,c.created_at,c.provider_id,c.generation_metadata,r.version,r.status,r.updated_at FROM device_routes r JOIN devices d ON d.id=r.device_id JOIN proxy_credentials c ON c.id=r.credential_id ` + where
	var r model.DeviceRoute
	var ingressEnc, enc, metadata []byte
	var providerID *string
	err := p.pool.QueryRow(ctx, q, args...).Scan(&r.DeviceID, &r.DeviceUsername, &r.PasswordHash, &r.IngressPort, &r.IngressMethod, &ingressEnc, &r.Credential.ID, &r.Credential.Host, &r.Credential.Port, &r.Credential.Username, &enc, &r.Credential.ExpiresAt, &r.Credential.CreatedAt, &providerID, &metadata, &r.Version, &r.Status, &r.UpdatedAt)
	if err != nil {
		return r, mapErr(err)
	}
	plain, err := p.cipher.Decrypt(enc)
	if err != nil {
		return r, err
	}
	r.Credential.Password = string(plain)
	if len(ingressEnc) > 0 {
		plain, err = p.cipher.Decrypt(ingressEnc)
		if err != nil {
			return r, err
		}
		r.IngressPassword = string(plain)
	}
	if providerID != nil {
		r.Credential.ProviderID = *providerID
	}
	if err = json.Unmarshal(metadata, &r.Credential.GenerationMetadata); err != nil {
		return r, err
	}
	r.Ingresses, err = p.loadDeviceIngresses(ctx, r.DeviceID)
	if err != nil {
		return r, err
	}
	return r, nil
}
func (p *Postgres) ListActiveRoutes(ctx context.Context) ([]model.DeviceRoute, error) {
	rows, err := p.pool.Query(ctx, `SELECT d.id,d.username,d.password_hash,COALESCE(d.ingress_port,0),d.ingress_method,COALESCE(d.ingress_password_cipher,'\x'::bytea),c.id,c.host,c.port,c.username,c.password_cipher,c.expires_at,c.created_at,c.provider_id,c.generation_metadata,r.version,r.status,r.updated_at FROM device_routes r JOIN devices d ON d.id=r.device_id JOIN proxy_credentials c ON c.id=r.credential_id WHERE r.status='active' ORDER BY d.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.DeviceRoute
	for rows.Next() {
		var r model.DeviceRoute
		var ingressEnc, enc, metadata []byte
		var providerID *string
		if err := rows.Scan(&r.DeviceID, &r.DeviceUsername, &r.PasswordHash, &r.IngressPort, &r.IngressMethod, &ingressEnc, &r.Credential.ID, &r.Credential.Host, &r.Credential.Port, &r.Credential.Username, &enc, &r.Credential.ExpiresAt, &r.Credential.CreatedAt, &providerID, &metadata, &r.Version, &r.Status, &r.UpdatedAt); err != nil {
			return nil, err
		}
		plain, err := p.cipher.Decrypt(enc)
		if err != nil {
			return nil, err
		}
		r.Credential.Password = string(plain)
		if len(ingressEnc) > 0 {
			plain, err = p.cipher.Decrypt(ingressEnc)
			if err != nil {
				return nil, err
			}
			r.IngressPassword = string(plain)
		}
		if providerID != nil {
			r.Credential.ProviderID = *providerID
		}
		if err = json.Unmarshal(metadata, &r.Credential.GenerationMetadata); err != nil {
			return nil, err
		}
		r.Ingresses, err = p.loadDeviceIngresses(ctx, r.DeviceID)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
func (p *Postgres) UpsertGateway(ctx context.Context, g model.Gateway) error {
	_, err := p.pool.Exec(ctx, `INSERT INTO gateways(id,address,status,applied_version,active_connections,last_heartbeat_at) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(id) DO UPDATE SET address=excluded.address,status=excluded.status,applied_version=excluded.applied_version,active_connections=excluded.active_connections,last_heartbeat_at=excluded.last_heartbeat_at`, g.ID, g.Address, g.Status, g.AppliedVersion, g.ActiveConnections, g.LastHeartbeatAt)
	return err
}
func (p *Postgres) ListGateways(ctx context.Context) ([]model.Gateway, error) {
	rows, err := p.pool.Query(ctx, `SELECT id,address,CASE WHEN last_heartbeat_at < now() - interval '15 seconds' THEN 'OFFLINE' ELSE status END,applied_version,active_connections,last_heartbeat_at FROM gateways ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Gateway
	for rows.Next() {
		var g model.Gateway
		if err := rows.Scan(&g.ID, &g.Address, &g.Status, &g.AppliedVersion, &g.ActiveConnections, &g.LastHeartbeatAt); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}
func (p *Postgres) AppendAudit(ctx context.Context, actor, action, resource string, details map[string]any) error {
	raw, err := json.Marshal(details)
	if err != nil {
		return err
	}
	_, err = p.pool.Exec(ctx, `INSERT INTO audit_events(actor,action,resource,details) VALUES($1,$2,$3,$4)`, actor, action, resource, raw)
	return err
}
func (p *Postgres) RecordRouteDeployment(ctx context.Context, deviceID string, version int64, gatewayID, phase string, success bool, message string) error {
	_, err := p.pool.Exec(ctx, `INSERT INTO route_deployments(device_id,route_version,gateway_id,phase,success,error,acknowledged_at) VALUES($1,$2,$3,$4,$5,$6,now()) ON CONFLICT(device_id,route_version,gateway_id,phase) DO UPDATE SET success=excluded.success,error=excluded.error,acknowledged_at=excluded.acknowledged_at`, deviceID, version, gatewayID, phase, success, message)
	return err
}
func mapErr(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrConflict
	}
	return err
}

func ApplyMigrations(ctx context.Context, pool *pgxpool.Pool, sql string) error {
	if _, err := pool.Exec(ctx, sql); err != nil {
		return fmt.Errorf("apply migration: %w", err)
	}
	return nil
}

package dataplane

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/proxymesh/proxymesh/internal/controlrpc"
	"github.com/proxymesh/proxymesh/internal/cryptox"
	"github.com/proxymesh/proxymesh/internal/model"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

type ControlClient struct {
	GatewayID, Address, ControlAddress, SnapshotPath string
	Table                                            *Table
	Gateway                                          *Gateway
	Cipher                                           *cryptox.Cipher
	Logger                                           *slog.Logger
	TLSCA, TLSCert, TLSKey, TLSServerName            string
}

func (c *ControlClient) Run(ctx context.Context) {
	backoff := time.Second
	for {
		if err := c.connect(ctx); err != nil && !errors.Is(err, context.Canceled) {
			c.Logger.Warn("control stream disconnected", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}
func (c *ControlClient) connect(ctx context.Context) error {
	creds, err := c.credentials()
	if err != nil {
		return err
	}
	conn, err := grpc.NewClient(c.ControlAddress, grpc.WithTransportCredentials(creds), grpc.WithDefaultCallOptions(grpc.CallContentSubtype("json")))
	if err != nil {
		return err
	}
	defer conn.Close()
	stream, err := controlrpc.NewGatewayControlClient(conn).Connect(ctx)
	if err != nil {
		return err
	}
	out := make(chan *controlrpc.Frame, 64)
	sendErr := make(chan error, 1)
	go func() {
		for {
			select {
			case f := <-out:
				if err := stream.Send(f); err != nil {
					sendErr <- err
					return
				}
			case <-ctx.Done():
				sendErr <- ctx.Err()
				return
			}
		}
	}()
	out <- &controlrpc.Frame{Type: controlrpc.TypeRegister, GatewayID: c.GatewayID}
	heartbeat := time.NewTicker(5 * time.Second)
	defer heartbeat.Stop()
	recv := make(chan *controlrpc.Frame)
	recvErr := make(chan error, 1)
	go func() {
		for {
			f, e := stream.Recv()
			if e != nil {
				recvErr <- e
				return
			}
			recv <- f
		}
	}()
	for {
		select {
		case f := <-recv:
			err := c.handle(f)
			ack := &controlrpc.Frame{Type: controlrpc.TypeAck, GatewayID: c.GatewayID, RequestID: f.RequestID}
			if err != nil {
				ack.Error = err.Error()
			}
			out <- ack
		case <-heartbeat.C:
			status := model.GatewaySyncing
			if c.Gateway.draining.Load() {
				status = model.GatewayDraining
			} else if c.Gateway.Ready() {
				status = model.GatewayReady
			}
			out <- &controlrpc.Frame{Type: controlrpc.TypeHeartbeat, GatewayID: c.GatewayID, Heartbeat: &model.Gateway{ID: c.GatewayID, Address: c.Address, Status: status, AppliedVersion: c.Table.MaxVersion(), ActiveConnections: c.Gateway.Connections.Total(), LastHeartbeatAt: time.Now().UTC()}}
		case err := <-recvErr:
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		case err := <-sendErr:
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
func (c *ControlClient) handle(f *controlrpc.Frame) error {
	switch f.Type {
	case controlrpc.TypeSnapshot:
		if err := SaveSnapshot(c.SnapshotPath, c.Cipher, f.Routes); err != nil {
			c.Gateway.SetControlSynced(false)
			return err
		}
		c.Table.Load(f.Routes)
		if err := c.Gateway.ReconcileListeners(); err != nil {
			c.Gateway.SetControlSynced(false)
			return err
		}
		c.Gateway.SetControlSynced(true)
		return nil
	case controlrpc.TypePrepare:
		if f.Route == nil {
			return errors.New("missing route")
		}
		if err := c.Gateway.PrepareListener(*f.Route); err != nil {
			return err
		}
		if err := c.Table.Prepare(*f.Route); err != nil {
			c.Gateway.SetControlSynced(false)
			return err
		}
		return nil
	case controlrpc.TypeActivate:
		if f.Route == nil {
			return errors.New("missing route")
		}
		if err := c.Table.Activate(*f.Route); err != nil {
			return err
		}
		if err := c.Gateway.ReconcileListeners(); err != nil {
			c.Gateway.SetControlSynced(false)
			return err
		}
		if err := SaveSnapshot(c.SnapshotPath, c.Cipher, c.Table.Snapshot()); err != nil {
			c.Gateway.SetControlSynced(false)
			return err
		}
		return nil
	case controlrpc.TypeDeviceUpdate:
		if f.Device == nil {
			return errors.New("missing device")
		}
		if err := c.Table.UpdateDevice(*f.Device); err != nil {
			return err
		}
		if err := c.Gateway.ReconcileListeners(); err != nil {
			c.Gateway.SetControlSynced(false)
			return err
		}
		if err := SaveSnapshot(c.SnapshotPath, c.Cipher, c.Table.Snapshot()); err != nil {
			c.Gateway.SetControlSynced(false)
			return err
		}
		return nil
	case controlrpc.TypeDevicePrepare:
		if f.Device == nil {
			return errors.New("missing device")
		}
		return c.Gateway.PrepareDevice(*f.Device)
	case controlrpc.TypeInterrupt:
		c.Gateway.Connections.Interrupt(f.DeviceID)
		return nil
	case controlrpc.TypeSetDraining:
		v, err := strconv.ParseBool(f.DeviceID)
		if err != nil {
			return err
		}
		c.Gateway.SetDraining(v)
		return nil
	default:
		return errors.New("unknown control frame")
	}
}
func (c *ControlClient) credentials() (credentials.TransportCredentials, error) {
	if c.TLSCA == "" {
		return insecure.NewCredentials(), nil
	}
	raw, err := os.ReadFile(c.TLSCA)
	if err != nil {
		return nil, err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(raw) {
		return nil, errors.New("invalid control CA")
	}
	cfg := &tls.Config{RootCAs: roots, ServerName: c.TLSServerName, MinVersion: tls.VersionTLS13}
	if c.TLSCert != "" {
		cert, err := tls.LoadX509KeyPair(c.TLSCert, c.TLSKey)
		if err != nil {
			return nil, err
		}
		cfg.Certificates = []tls.Certificate{cert}
	}
	return credentials.NewTLS(cfg), nil
}

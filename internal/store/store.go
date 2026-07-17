package store

import (
	"context"
	"errors"

	"github.com/proxymesh/proxymesh/internal/model"
	"github.com/proxymesh/proxymesh/internal/provider"
)

var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("version conflict")
)

type Store interface {
	CreateDevice(context.Context, model.Device) error
	GetDevice(context.Context, string) (model.Device, error)
	UpdateDeviceCredential(context.Context, string, string) (model.Device, error)
	AddDeviceIngress(context.Context, string, model.DeviceIngress) (model.Device, error)
	DeleteDeviceIngress(context.Context, string, int) (model.Device, error)
	UpsertProvider(context.Context, provider.Definition) error
	GetProvider(context.Context, string) (provider.Definition, error)
	ListProviders(context.Context) ([]provider.Definition, error)
	StageRoute(context.Context, model.DeviceRoute, int64) error
	ActivateRoute(context.Context, string, int64) error
	GetRoute(context.Context, string) (model.DeviceRoute, error)
	ListActiveRoutes(context.Context) ([]model.DeviceRoute, error)
	UpsertGateway(context.Context, model.Gateway) error
	ListGateways(context.Context) ([]model.Gateway, error)
	RecordRouteDeployment(context.Context, string, int64, string, string, bool, string) error
	AppendAudit(context.Context, string, string, string, map[string]any) error
	Close()
}

package controlrpc

import (
	"context"
	"encoding/json"

	"github.com/proxymesh/proxymesh/internal/model"
	"google.golang.org/grpc"
	"google.golang.org/grpc/encoding"
)

type Codec struct{}

func (Codec) Name() string                    { return "json" }
func (Codec) Marshal(v any) ([]byte, error)   { return json.Marshal(v) }
func (Codec) Unmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }
func init()                                   { encoding.RegisterCodec(Codec{}) }

type Frame struct {
	Type      string              `json:"type"`
	GatewayID string              `json:"gatewayId,omitempty"`
	RequestID string              `json:"requestId,omitempty"`
	Route     *model.DeviceRoute  `json:"route,omitempty"`
	Routes    []model.DeviceRoute `json:"routes,omitempty"`
	Device    *model.Device       `json:"device,omitempty"`
	Heartbeat *model.Gateway      `json:"heartbeat,omitempty"`
	DeviceID  string              `json:"deviceId,omitempty"`
	Error     string              `json:"error,omitempty"`
}

const (
	TypeRegister      = "register"
	TypeSnapshot      = "snapshot"
	TypePrepare       = "prepare"
	TypeActivate      = "activate"
	TypeAck           = "ack"
	TypeHeartbeat     = "heartbeat"
	TypeDevicePrepare = "device_prepare"
	TypeDeviceUpdate  = "device_update"
	TypeInterrupt     = "interrupt"
	TypeSetDraining   = "set_draining"
)

type GatewayControlServer interface {
	Connect(GatewayControl_ConnectServer) error
}
type GatewayControl_ConnectServer interface {
	Send(*Frame) error
	Recv() (*Frame, error)
	grpc.ServerStream
}
type connectServer struct{ grpc.ServerStream }

func (s *connectServer) Send(f *Frame) error { return s.SendMsg(f) }
func (s *connectServer) Recv() (*Frame, error) {
	f := new(Frame)
	if err := s.RecvMsg(f); err != nil {
		return nil, err
	}
	return f, nil
}

func RegisterGatewayControlServer(s grpc.ServiceRegistrar, server GatewayControlServer) {
	s.RegisterService(&GatewayControl_ServiceDesc, server)
}
func connectHandler(srv any, stream grpc.ServerStream) error {
	return srv.(GatewayControlServer).Connect(&connectServer{stream})
}

var GatewayControl_ServiceDesc = grpc.ServiceDesc{ServiceName: "proxymesh.control.v1.GatewayControl", HandlerType: (*GatewayControlServer)(nil), Streams: []grpc.StreamDesc{{StreamName: "Connect", Handler: connectHandler, ServerStreams: true, ClientStreams: true}}}

type GatewayControlClient interface {
	Connect(context.Context, ...grpc.CallOption) (GatewayControl_ConnectClient, error)
}
type gatewayControlClient struct{ cc grpc.ClientConnInterface }

func NewGatewayControlClient(cc grpc.ClientConnInterface) GatewayControlClient {
	return &gatewayControlClient{cc}
}

type GatewayControl_ConnectClient interface {
	Send(*Frame) error
	Recv() (*Frame, error)
	CloseSend() error
	grpc.ClientStream
}
type connectClient struct{ grpc.ClientStream }

func (c *gatewayControlClient) Connect(ctx context.Context, opts ...grpc.CallOption) (GatewayControl_ConnectClient, error) {
	opts = append(opts, grpc.CallContentSubtype("json"))
	stream, err := c.cc.NewStream(ctx, &GatewayControl_ServiceDesc.Streams[0], "/proxymesh.control.v1.GatewayControl/Connect", opts...)
	if err != nil {
		return nil, err
	}
	return &connectClient{stream}, nil
}
func (c *connectClient) Send(f *Frame) error { return c.SendMsg(f) }
func (c *connectClient) Recv() (*Frame, error) {
	f := new(Frame)
	if err := c.RecvMsg(f); err != nil {
		return nil, err
	}
	return f, nil
}

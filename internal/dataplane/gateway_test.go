package dataplane

import (
	"bufio"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	sscore "github.com/go-gost/go-shadowsocks2/core"
	"github.com/go-gost/go-shadowsocks2/shadowaead"
	sssocks "github.com/go-gost/go-shadowsocks2/socks"
	ssutils "github.com/go-gost/go-shadowsocks2/utils"
	"github.com/proxymesh/proxymesh/internal/model"
)

func TestConnectThroughAuthenticatedSOCKS5(t *testing.T) {
	testConnectThroughAuthenticatedSOCKS5(t, "aes-256-gcm", "device-pass")
}

func TestConnectThroughAuthenticatedSOCKS5WithShadowsocks2022(t *testing.T) {
	testConnectThroughAuthenticatedSOCKS5(t, "2022-blake3-aes-128-gcm", "MDEyMzQ1Njc4OWFiY2RlZg==")
}

func testConnectThroughAuthenticatedSOCKS5(t *testing.T, ingressMethod, ingressPassword string) {
	t.Helper()
	echoAddr, closeEcho := startEcho(t)
	defer closeEcho()
	socksAddr, closeSOCKS := startSOCKS5(t, "up-user", "up-pass")
	defer closeSOCKS()
	ingressPort := freePort(t)
	table := NewTable()
	table.Load([]model.DeviceRoute{{DeviceID: "device-001", IngressPort: ingressPort, IngressMethod: ingressMethod, IngressPassword: ingressPassword, Version: 1, Status: model.RouteActive, Credential: model.ProxyCredential{Host: host(socksAddr), Port: port(socksAddr), Username: "up-user", Password: "up-pass"}}})
	gateway := NewGateway(table, NewConnections(), 2*time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))
	gateway.SetListenHost("127.0.0.1")
	gateway.SetControlSynced(true)
	if err := gateway.ReconcileListeners(); err != nil {
		t.Fatal(err)
	}
	defer gateway.CloseListeners()
	assertShadowsocksEcho(t, ingressPort, ingressMethod, ingressPassword, echoAddr)
}

func TestLegacyAndSS2022IngressesShareOneDeviceRoute(t *testing.T) {
	echoAddr, closeEcho := startEcho(t)
	defer closeEcho()
	socksAddr, closeSOCKS := startSOCKS5(t, "up-user", "up-pass")
	defer closeSOCKS()
	legacyPort, ss2022Port := freePort(t), freePort(t)
	for ss2022Port == legacyPort {
		ss2022Port = freePort(t)
	}
	legacy := model.DeviceIngress{Port: legacyPort, Method: "aes-256-gcm", Password: "device-pass", Primary: true}
	ss2022 := model.DeviceIngress{Port: ss2022Port, Method: "2022-blake3-aes-128-gcm", Password: "MDEyMzQ1Njc4OWFiY2RlZg=="}
	table := NewTable()
	table.Load([]model.DeviceRoute{{DeviceID: "device-001", IngressPort: legacy.Port, IngressMethod: legacy.Method, IngressPassword: legacy.Password, Ingresses: []model.DeviceIngress{legacy, ss2022}, Version: 1, Status: model.RouteActive, Credential: model.ProxyCredential{Host: host(socksAddr), Port: port(socksAddr), Username: "up-user", Password: "up-pass"}}})
	gateway := NewGateway(table, NewConnections(), 2*time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))
	gateway.SetListenHost("127.0.0.1")
	gateway.SetControlSynced(true)
	if err := gateway.ReconcileListeners(); err != nil {
		t.Fatal(err)
	}
	defer gateway.CloseListeners()
	assertShadowsocksEcho(t, legacy.Port, legacy.Method, legacy.Password, echoAddr)
	assertShadowsocksEcho(t, ss2022.Port, ss2022.Method, ss2022.Password, echoAddr)
	if len(gateway.listeners) != 2 {
		t.Fatalf("listeners=%d", len(gateway.listeners))
	}
}

func assertShadowsocksEcho(t *testing.T, ingressPort int, ingressMethod, ingressPassword, echoAddr string) {
	t.Helper()
	raw, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", fmt.Sprint(ingressPort)))
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	clientConfig, err := ssutils.NewClientConfig(ingressMethod, ingressPassword)
	if err != nil {
		t.Fatal(err)
	}
	if ingressMethod == "aes-256-gcm" {
		exchangeLegacyShadowsocks(t, raw, clientConfig.Cipher, echoAddr)
		return
	}
	client := sscore.NewTCPClient(clientConfig)
	secured, err := client.WrapConn(raw, sssocks.ParseAddr(echoAddr))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = secured.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 4)
	if _, err = io.ReadFull(secured, reply); err != nil {
		t.Fatal(err)
	}
	if string(reply) != "ping" {
		t.Fatalf("reply=%q", reply)
	}
}

func exchangeLegacyShadowsocks(t *testing.T, raw net.Conn, cipher sscore.ShadowCipher, target string) {
	t.Helper()
	salt := make([]byte, cipher.SaltSize())
	if _, err := rand.Read(salt); err != nil {
		t.Fatal(err)
	}
	encrypter, err := cipher.Encrypter(nil, salt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = raw.Write(salt); err != nil {
		t.Fatal(err)
	}
	payload := append([]byte{}, sssocks.ParseAddr(target)...)
	payload = append(payload, []byte("ping")...)
	if _, err = shadowaead.NewWriter(raw, encrypter).Write(payload); err != nil {
		t.Fatal(err)
	}
	responseSalt := make([]byte, cipher.SaltSize())
	if _, err = io.ReadFull(raw, responseSalt); err != nil {
		t.Fatal(err)
	}
	decrypter, err := cipher.Decrypter(nil, responseSalt)
	if err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 4)
	if _, err = io.ReadFull(shadowaead.NewReader(raw, decrypter), reply); err != nil {
		t.Fatal(err)
	}
	if string(reply) != "ping" {
		t.Fatalf("reply=%q", reply)
	}
}

func TestReadinessRequiresControlSyncAndLocalDrain(t *testing.T) {
	table := NewTable()
	table.Load(nil)
	gateway := NewGateway(table, NewConnections(), time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if gateway.Ready() {
		t.Fatal("snapshot without control reconciliation must not be ready")
	}
	gateway.SetControlSynced(true)
	if !gateway.Ready() {
		t.Fatal("reconciled gateway should be ready")
	}
	req := httptest.NewRequest(http.MethodPost, "/drain", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	rec := httptest.NewRecorder()
	gateway.HealthHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent || gateway.Ready() {
		t.Fatalf("drain status=%d ready=%v", rec.Code, gateway.Ready())
	}
}

func TestUnsupportedShadowsocksMethodRejected(t *testing.T) {
	gateway := NewGateway(NewTable(), NewConnections(), time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))
	err := gateway.ValidateRoute(model.DeviceRoute{IngressPort: 50000, IngressMethod: "unsupported", IngressPassword: "secret"})
	if err == nil {
		t.Fatal("unsupported Shadowsocks method was accepted")
	}
}

func TestRouteUpdateKeepsDeviceListener(t *testing.T) {
	ingressPort := freePort(t)
	table := NewTable()
	v1 := model.DeviceRoute{DeviceID: "device-001", IngressPort: ingressPort, IngressMethod: "aes-256-gcm", IngressPassword: "device-pass", Version: 1, Status: model.RouteActive, Credential: model.ProxyCredential{Host: "127.0.0.1", Port: 1080}}
	table.Load([]model.DeviceRoute{v1})
	gateway := NewGateway(table, NewConnections(), time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))
	gateway.SetListenHost("127.0.0.1")
	if err := gateway.ReconcileListeners(); err != nil {
		t.Fatal(err)
	}
	defer gateway.CloseListeners()
	before := gateway.listeners[ingressPort].listener
	v2 := v1
	v2.Version = 2
	v2.Credential.Host = "192.0.2.10"
	if err := table.Prepare(v2); err != nil {
		t.Fatal(err)
	}
	if err := table.Activate(v2); err != nil {
		t.Fatal(err)
	}
	if err := gateway.ReconcileListeners(); err != nil {
		t.Fatal(err)
	}
	if after := gateway.listeners[ingressPort].listener; after != before {
		t.Fatal("upstream route update restarted the device Shadowsocks listener")
	}
}

func TestPreparedIngressRemainsInactiveUntilDeviceActivation(t *testing.T) {
	legacyPort, ss2022Port := freePort(t), freePort(t)
	for ss2022Port == legacyPort {
		ss2022Port = freePort(t)
	}
	legacy := model.DeviceIngress{Port: legacyPort, Method: "aes-256-gcm", Password: "device-pass", Primary: true}
	ss2022 := model.DeviceIngress{Port: ss2022Port, Method: "2022-blake3-aes-128-gcm", Password: "MDEyMzQ1Njc4OWFiY2RlZg=="}
	table := NewTable()
	table.Load([]model.DeviceRoute{{DeviceID: "device-001", IngressPort: legacy.Port, IngressMethod: legacy.Method, IngressPassword: legacy.Password, Ingresses: []model.DeviceIngress{legacy}, Version: 1, Status: model.RouteActive}})
	gateway := NewGateway(table, NewConnections(), time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))
	gateway.SetListenHost("127.0.0.1")
	if err := gateway.ReconcileListeners(); err != nil {
		t.Fatal(err)
	}
	defer gateway.CloseListeners()
	proposed := model.Device{ID: "device-001", IngressPort: legacy.Port, IngressMethod: legacy.Method, IngressPassword: legacy.Password, Ingresses: []model.DeviceIngress{legacy, ss2022}}
	if err := gateway.PrepareDevice(proposed); err != nil {
		t.Fatal(err)
	}
	if gateway.listeners[ss2022Port].active.Load() {
		t.Fatal("prepared ingress accepted traffic before activation")
	}
	if err := table.UpdateDevice(proposed); err != nil {
		t.Fatal(err)
	}
	if err := gateway.ReconcileListeners(); err != nil {
		t.Fatal(err)
	}
	if !gateway.listeners[ss2022Port].active.Load() {
		t.Fatal("activated ingress remained inactive")
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := port(ln.Addr().String())
	_ = ln.Close()
	return p
}

func startEcho(t *testing.T) (string, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			c, e := ln.Accept()
			if e != nil {
				return
			}
			go func() { defer c.Close(); _, _ = io.Copy(c, c) }()
		}
	}()
	return ln.Addr().String(), func() { ln.Close() }
}
func startSOCKS5(t *testing.T, user, pass string) (string, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			c, e := ln.Accept()
			if e != nil {
				return
			}
			go serveSOCKS(c, user, pass)
		}
	}()
	return ln.Addr().String(), func() { ln.Close() }
}
func serveSOCKS(c net.Conn, user, pass string) {
	defer c.Close()
	br := bufio.NewReader(c)
	head := make([]byte, 2)
	if _, e := io.ReadFull(br, head); e != nil {
		return
	}
	methods := make([]byte, int(head[1]))
	if _, e := io.ReadFull(br, methods); e != nil {
		return
	}
	_, _ = c.Write([]byte{5, 2})
	if _, e := io.ReadFull(br, head); e != nil {
		return
	}
	u := make([]byte, int(head[1]))
	io.ReadFull(br, u)
	pLen, e := br.ReadByte()
	if e != nil {
		return
	}
	p := make([]byte, int(pLen))
	io.ReadFull(br, p)
	if string(u) != user || string(p) != pass {
		_, _ = c.Write([]byte{1, 1})
		return
	}
	_, _ = c.Write([]byte{1, 0})
	req := make([]byte, 4)
	if _, e = io.ReadFull(br, req); e != nil {
		return
	}
	var h string
	switch req[3] {
	case 1:
		b := make([]byte, 4)
		io.ReadFull(br, b)
		h = net.IP(b).String()
	case 3:
		n, _ := br.ReadByte()
		b := make([]byte, int(n))
		io.ReadFull(br, b)
		h = string(b)
	default:
		return
	}
	pb := make([]byte, 2)
	io.ReadFull(br, pb)
	target := net.JoinHostPort(h, fmt.Sprint(binary.BigEndian.Uint16(pb)))
	up, e := net.Dial("tcp", target)
	if e != nil {
		_, _ = c.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	defer up.Close()
	_, _ = c.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0})
	done := make(chan struct{}, 2)
	go func() { io.Copy(up, br); done <- struct{}{} }()
	go func() { io.Copy(c, up); done <- struct{}{} }()
	<-done
}
func host(addr string) string { h, _, _ := net.SplitHostPort(addr); return h }
func port(addr string) int    { _, p, _ := net.SplitHostPort(addr); var n int; fmt.Sscan(p, &n); return n }

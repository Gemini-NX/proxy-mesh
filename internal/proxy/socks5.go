package proxy

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

type SOCKS5Config struct{ Address, Username, Password string }

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.reader.Read(p) }

func DialSOCKS5(ctx context.Context, cfg SOCKS5Config, target string) (net.Conn, error) {
	d := net.Dialer{}
	conn, err := d.DialContext(ctx, "tcp", cfg.Address)
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			conn.Close()
		}
	}()
	if deadline, hasDeadline := ctx.Deadline(); hasDeadline {
		if err = conn.SetDeadline(deadline); err != nil {
			return nil, err
		}
	}
	methods := []byte{0x00}
	if cfg.Username != "" || cfg.Password != "" {
		methods = []byte{0x02}
	}
	if _, err = conn.Write(append([]byte{0x05, byte(len(methods))}, methods...)); err != nil {
		return nil, err
	}
	reply := make([]byte, 2)
	if _, err = io.ReadFull(conn, reply); err != nil {
		return nil, err
	}
	expectedMethod := byte(0x00)
	if cfg.Username != "" || cfg.Password != "" {
		expectedMethod = 0x02
	}
	if reply[0] != 0x05 || reply[1] != expectedMethod {
		return nil, errors.New("SOCKS5 authentication method rejected")
	}
	if reply[1] == 0x02 {
		if len(cfg.Username) > 255 || len(cfg.Password) > 255 {
			return nil, errors.New("SOCKS5 credentials too long")
		}
		auth := []byte{0x01, byte(len(cfg.Username))}
		auth = append(auth, []byte(cfg.Username)...)
		auth = append(auth, byte(len(cfg.Password)))
		auth = append(auth, []byte(cfg.Password)...)
		if _, err = conn.Write(auth); err != nil {
			return nil, err
		}
		if _, err = io.ReadFull(conn, reply); err != nil {
			return nil, err
		}
		if reply[0] != 0x01 || reply[1] != 0 {
			return nil, errors.New("SOCKS5 username/password rejected")
		}
	}
	host, portText, err := net.SplitHostPort(target)
	if err != nil {
		return nil, fmt.Errorf("invalid target: %w", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return nil, errors.New("invalid target port")
	}
	req := []byte{0x05, 0x01, 0x00}
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			req = append(req, 0x01)
			req = append(req, v4...)
		} else {
			req = append(req, 0x04)
			req = append(req, ip.To16()...)
		}
	} else {
		if len(host) > 255 {
			return nil, errors.New("target hostname too long")
		}
		req = append(req, 0x03, byte(len(host)))
		req = append(req, host...)
	}
	p := make([]byte, 2)
	binary.BigEndian.PutUint16(p, uint16(port))
	req = append(req, p...)
	if _, err = conn.Write(req); err != nil {
		return nil, err
	}
	br := bufio.NewReader(conn)
	head := make([]byte, 4)
	if _, err = io.ReadFull(br, head); err != nil {
		return nil, err
	}
	if head[0] != 0x05 || head[1] != 0 {
		return nil, fmt.Errorf("SOCKS5 connect failed: %s", replyMessage(head[1]))
	}
	var n int
	switch head[3] {
	case 0x01:
		n = 4
	case 0x04:
		n = 16
	case 0x03:
		b, e := br.ReadByte()
		if e != nil {
			return nil, e
		}
		n = int(b)
	default:
		return nil, errors.New("invalid SOCKS5 address type")
	}
	discard := make([]byte, n+2)
	if _, err = io.ReadFull(br, discard); err != nil {
		return nil, err
	}
	ok = true
	if err = conn.SetDeadline(time.Time{}); err != nil {
		return nil, err
	}
	return &bufferedConn{Conn: conn, reader: br}, nil
}
func replyMessage(code byte) string {
	messages := []string{"succeeded", "general failure", "not allowed", "network unreachable", "host unreachable", "connection refused", "TTL expired", "command unsupported", "address unsupported"}
	if int(code) < len(messages) {
		return messages[code]
	}
	return "unknown error " + strings.ToUpper(strconv.FormatInt(int64(code), 16))
}

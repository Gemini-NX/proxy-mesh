package ssconfig

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"

	ssutils "github.com/go-gost/go-shadowsocks2/utils"
)

const (
	LegacyAES256GCM = "aes-256-gcm"
	Blake3AES128GCM = "2022-blake3-aes-128-gcm"
)

func Validate(method, password string) error {
	switch method {
	case LegacyAES256GCM, Blake3AES128GCM:
	default:
		return fmt.Errorf("unsupported Shadowsocks method %q", method)
	}
	if password == "" {
		return fmt.Errorf("Shadowsocks password is required")
	}
	if method == LegacyAES256GCM && len(password) < 8 {
		return fmt.Errorf("legacy Shadowsocks password must contain at least 8 characters")
	}
	if _, err := ssutils.NewServerConfig(method, password, nil); err != nil {
		return fmt.Errorf("invalid %s password: %w", method, err)
	}
	return nil
}

func GeneratePassword(method string) (string, error) {
	size := 18
	encoding := base64.RawURLEncoding
	if method == Blake3AES128GCM {
		size = 16
		encoding = base64.StdEncoding
	} else if method != LegacyAES256GCM {
		return "", fmt.Errorf("unsupported Shadowsocks method %q", method)
	}
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return encoding.EncodeToString(b), nil
}

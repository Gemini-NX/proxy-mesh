package ssconfig

import (
	"encoding/base64"
	"testing"
)

func TestGenerateSS2022Password(t *testing.T) {
	password, err := GeneratePassword(Blake3AES128GCM)
	if err != nil {
		t.Fatal(err)
	}
	key, err := base64.StdEncoding.DecodeString(password)
	if err != nil || len(key) != 16 {
		t.Fatalf("invalid generated SS2022 key: bytes=%d err=%v", len(key), err)
	}
	if err = Validate(Blake3AES128GCM, password); err != nil {
		t.Fatal(err)
	}
}

func TestRejectInvalidSS2022Password(t *testing.T) {
	if err := Validate(Blake3AES128GCM, "ordinary-password"); err == nil {
		t.Fatal("invalid SS2022 key was accepted")
	}
}

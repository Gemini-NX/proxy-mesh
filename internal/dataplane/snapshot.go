package dataplane

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/proxymesh/proxymesh/internal/cryptox"
	"github.com/proxymesh/proxymesh/internal/model"
)

func SaveSnapshot(path string, c *cryptox.Cipher, routes []model.DeviceRoute) error {
	raw, err := json.Marshal(routes)
	if err != nil {
		return err
	}
	enc, err := c.Encrypt(raw)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err = os.WriteFile(tmp, enc, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
func LoadSnapshot(path string, c *cryptox.Cipher) ([]model.DeviceRoute, error) {
	enc, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	raw, err := c.Decrypt(enc)
	if err != nil {
		return nil, err
	}
	var routes []model.DeviceRoute
	if err = json.Unmarshal(raw, &routes); err != nil {
		return nil, err
	}
	return routes, nil
}

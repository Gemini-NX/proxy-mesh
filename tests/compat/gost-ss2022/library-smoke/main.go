package main

import (
	"fmt"

	"github.com/go-gost/go-shadowsocks2/utils"
)

func main() {
	for _, tc := range []struct {
		method   string
		password string
	}{
		{method: "aes-256-gcm", password: "legacy-test-password"},
		{method: "2022-blake3-aes-128-gcm", password: "MDEyMzQ1Njc4OWFiY2RlZg=="},
	} {
		if _, err := utils.NewServerConfig(tc.method, tc.password, nil); err != nil {
			panic(fmt.Sprintf("%s: %v", tc.method, err))
		}
		fmt.Println(tc.method + ": supported")
	}
}

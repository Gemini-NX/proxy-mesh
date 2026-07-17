package migrations

import _ "embed"

//go:embed 001_initial.sql
var Initial string

//go:embed 002_proxy_providers.sql
var Providers string

//go:embed 003_shadowsocks_ingress.sql
var ShadowsocksIngress string

//go:embed 004_device_ingresses.sql
var DeviceIngresses string

var All = Initial + "\n" + Providers + "\n" + ShadowsocksIngress + "\n" + DeviceIngresses

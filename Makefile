.PHONY: test vet build compose-up compose-down local-up

test:
	go test ./...

vet:
	go vet ./...

build:
	go build ./apps/control-plane ./apps/gateway

compose-up:
	docker compose up --build

local-up:
	./scripts/local-up.sh

compose-down:
	docker compose down

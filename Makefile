COMPOSE := docker compose -f deploy/docker-compose.yml

.PHONY: up down logs build test tidy vet smoke

bin/captain: $(shell find . -name '*.go' -not -path './bin/*')
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o bin/captain ./cmd/server

up: bin/captain ## 本地一键拉起全栈（postgres/redis/nats/captain）
	DOCKER_BUILDKIT=0 $(COMPOSE) up --build -d
	@echo "captain -> http://localhost:8080  (healthz: /healthz)"
	@echo "查看 demo 入场链接: make logs | grep seed"

down:
	$(COMPOSE) down

logs:
	$(COMPOSE) logs -f captain

build:
	go build ./...

vet:
	go vet ./...

test:
	go test ./...

tidy:
	go mod tidy

smoke:     ## 端到端冒烟：扫码→签到→计数→导出→下载
	bash scripts/smoke.sh

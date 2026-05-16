COMPOSE := docker compose -f deploy/docker-compose.yml

.PHONY: up down logs build test tidy vet smoke

up:        ## 本地一键拉起全栈（postgres/redis/nats/captain）
	$(COMPOSE) up --build -d
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

COMPOSE := docker compose -f deploy/docker-compose.yml

.PHONY: up down logs build test tidy vet smoke

bin/captain: $(shell find . -name '*.go' -not -path './bin/*')
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o bin/captain ./cmd/server

deploy/.env: ## 生成强随机密钥/口令/混淆路径（gitignored，仅首次）
	@umask 077; { \
	  echo "CAPTAIN_TOKEN_SECRET=$$(openssl rand -hex 32)"; \
	  echo "CAPTAIN_IDENTITY_PEPPER=$$(openssl rand -hex 32)"; \
	  echo "CAPTAIN_ADMIN_PATH=mgmt-$$(openssl rand -hex 4)"; \
	  echo "CAPTAIN_SEED_ADMIN_PW=$$(openssl rand -base64 18 | tr -d /+= )"; \
	  echo "CAPTAIN_SEED_ORG_PW=$$(openssl rand -base64 18 | tr -d /+= )"; \
	} > deploy/.env
	@echo "deploy/.env 已生成（强随机）。管理后台路径与口令见容器日志：make logs | grep seed/admin"

up: bin/captain deploy/.env ## 本地一键拉起全栈（postgres/redis/nats/captain）
	DOCKER_BUILDKIT=0 $(COMPOSE) --env-file deploy/.env up --build -d
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

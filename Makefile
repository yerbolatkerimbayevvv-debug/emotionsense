.DEFAULT_GOAL := help
SHELL := /bin/bash

APP        := emotionsense
COMPOSE    := docker compose -f deploy/docker-compose.yml
INFRA      := docker compose -f deploy/docker-compose.infra.yml
GOBIN      := $(shell go env GOPATH)/bin
MIGRATE_DSN ?= postgres://emotion:emotion@localhost:5432/emotionsense?sslmode=disable

## ---------- Разработка ----------

.PHONY: run-api
run-api: ## Запустить API локально (инфраструктура должна быть поднята: make infra-up)
	go run ./cmd/api

.PHONY: run-worker
run-worker: ## Запустить worker локально
	HTTP_PORT=8081 go run ./cmd/worker

.PHONY: build
build: ## Собрать бинарники в ./bin
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/api ./cmd/api
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/worker ./cmd/worker

.PHONY: tidy
tidy: ## go mod tidy
	go mod tidy

.PHONY: fmt
fmt: ## Форматирование
	gofmt -s -w .

## ---------- Качество ----------

.PHONY: test
test: ## Unit-тесты
	go test -race -count=1 ./...

.PHONY: test-cover
test-cover: ## Тесты с отчётом покрытия
	go test -race -count=1 -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

.PHONY: test-integration
test-integration: ## Интеграционные тесты (нужен Postgres: make infra-up)
	TEST_POSTGRES_DSN="postgres://emotion:emotion@localhost:5432/emotionsense?sslmode=disable" \
		go test -race -count=1 -run Integration ./internal/repository/...

.PHONY: lint
lint: ## golangci-lint (установка: make tools)
	$(GOBIN)/golangci-lint run ./...

.PHONY: vet
vet: ## go vet
	go vet ./...

## ---------- Кодогенерация ----------

.PHONY: proto
proto: ## Сгенерировать Go-код из proto (нужен protoc + плагины: make tools)
	protoc -I proto \
		--go_out=gen --go_opt=paths=source_relative \
		--go-grpc_out=gen --go-grpc_opt=paths=source_relative \
		proto/emotion/v1/emotion.proto

.PHONY: tools
tools: ## Установить инструменты разработки
	go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.6
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.5.1
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@v1.64.8
	go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@v4.18.2

## ---------- Миграции (CLI golang-migrate; сервисы также мигрируют при старте) ----------

.PHONY: migrate-up
migrate-up: ## Применить все миграции
	$(GOBIN)/migrate -path migrations -database "$(MIGRATE_DSN)" up

.PHONY: migrate-down
migrate-down: ## Откатить одну миграцию
	$(GOBIN)/migrate -path migrations -database "$(MIGRATE_DSN)" down 1

.PHONY: migrate-new
migrate-new: ## Создать новую миграцию: make migrate-new name=add_something
	$(GOBIN)/migrate create -ext sql -dir migrations -seq $(name)

## ---------- Docker ----------

.PHONY: infra-up
infra-up: ## Поднять только инфраструктуру + ML (для go run с хоста)
	$(INFRA) up -d --build

.PHONY: infra-down
infra-down: ## Остановить инфраструктуру
	$(INFRA) down

.PHONY: up
up: ## Поднять весь стек (api, worker x2, ml, postgres, redis, minio, kafka)
	$(COMPOSE) up -d --build

.PHONY: up-tools
up-tools: ## То же + Kafka UI на :8090
	$(COMPOSE) --profile tools up -d --build

.PHONY: down
down: ## Остановить стек
	$(COMPOSE) down

.PHONY: down-v
down-v: ## Остановить стек и удалить данные
	$(COMPOSE) down -v

.PHONY: logs
logs: ## Логи всех сервисов
	$(COMPOSE) logs -f --tail=100

.PHONY: ps
ps: ## Статус контейнеров
	$(COMPOSE) ps

## ---------- Демо ----------

.PHONY: demo
demo: ## Прогнать сценарий: регистрация → загрузка фото → ожидание результата
	./scripts/demo.sh $(IMG)

.PHONY: help
help: ## Показать эту справку
	@grep -hE '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

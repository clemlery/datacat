-include .env
export

COMPOSE   := podman compose
LOCAL_DSN  = postgres://$(POSTGRES_USER):$(POSTGRES_PASSWORD)@127.0.0.1:5432/$(POSTGRES_DB)?sslmode=disable
# Les $$ sont évalués dans le conteneur db, où ces variables existent.
PSQL      := sh -c 'psql -v ON_ERROR_STOP=1 -U "$$POSTGRES_USER" -d "$$POSTGRES_DB"'

.PHONY: up down logs run build test lint seed reset migrate-new psql check-env

check-env:
	@test -f .env || { echo "Fichier .env absent : cp .env.example .env" >&2; exit 1; }

up: check-env
	$(COMPOSE) up -d --build

down:
	$(COMPOSE) down

logs:
	$(COMPOSE) logs -f

run: check-env
	@if [ -n "$$($(COMPOSE) ps -q --status running api 2>/dev/null)" ]; then \
		echo "Le service api tourne déjà (conflit sur le port 8080) : podman compose stop api" >&2; exit 1; \
	fi
	$(COMPOSE) up -d --wait db
	DATABASE_URL='$(LOCAL_DSN)' go run ./cmd/datacat

build:
	CGO_ENABLED=0 go build -trimpath -o bin/datacat ./cmd/datacat

test:
	go test -race ./...

lint:
	@command -v golangci-lint >/dev/null || { echo "golangci-lint n'est pas installé sur l'hôte" >&2; exit 1; }
	golangci-lint run

seed:
	$(COMPOSE) exec -T db $(PSQL) < scripts/seed.sql

# DESTRUCTIF : supprime le volume et donc toutes les données.
reset: check-env
	$(COMPOSE) down -v
	$(COMPOSE) up -d --build --wait
	$(MAKE) seed

migrate-new:
	@test -n "$(name)" || { echo "usage : make migrate-new name=<description>" >&2; exit 1; }
	go tool migrate create -ext sql -dir migrations -seq -digits 6 $(name)

psql:
	$(COMPOSE) exec db sh -c 'psql -U "$$POSTGRES_USER" -d "$$POSTGRES_DB"'

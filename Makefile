COMPOSE := docker compose -f deploy/docker-compose.yml
GOIMAGE := golang:1.26-alpine
GORUN := docker run --rm -v $(CURDIR)/backend:/src -w /src -u $(shell id -u):$(shell id -g) -e HOME=/tmp $(GOIMAGE)

.DEFAULT_GOAL := help
.PHONY: help up down build rebuild logs ps restart clean test e2e backup restore

help: ## show this help
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

up: ## build and start the full stack
	$(COMPOSE) up -d --build
	@PORT=$$(grep -E '^ARGUS_WEB_PORT=' deploy/.env 2>/dev/null | cut -d= -f2); \
		echo ""; echo "  Argus is running: http://localhost:$${PORT:-8080}"

down: ## stop the stack (data is kept)
	$(COMPOSE) down

build: ## build all images
	$(COMPOSE) build

rebuild: ## rebuild images from scratch (no cache)
	$(COMPOSE) build --no-cache

logs: ## follow stack logs
	$(COMPOSE) logs -f --tail=100

ps: ## show stack status
	$(COMPOSE) ps

restart: ## restart all services
	$(COMPOSE) restart

clean: ## stop the stack and DELETE the database
	$(COMPOSE) down -v

test: ## run backend vet + tests (in Docker)
	$(GORUN) sh -c "go vet ./... && go test -p 1 ./..."

e2e: ## compose smoke (stack must already be up on ARGUS_WEB_PORT)
	@set -a; [ ! -f deploy/.env ] || . ./deploy/.env; set +a; \
		ARGUS_BASE_URL="$${ARGUS_BASE_URL:-http://localhost:$${ARGUS_WEB_PORT:-8080}}" ./tests/e2e/smoke.sh

backup: ## dump Postgres to backups/argus-YYYYMMDD-HHMMSS.dump
	mkdir -p backups
	$(COMPOSE) exec -T db sh -c 'pg_dump -U "$$POSTGRES_USER" -d "$$POSTGRES_DB" -Fc' > backups/argus-$$(date +%Y%m%d-%H%M%S).dump
	@ls -1t backups/argus-*.dump | head -n 1 | awk '{print "  wrote " $$0}'

restore: ## restore FILE=backups/foo.dump (replaces the database)
	@test -n "$(FILE)" || (echo "usage: make restore FILE=backups/argus-YYYYMMDD-HHMMSS.dump" && exit 1)
	@test -f "$(FILE)" || (echo "restore: $(FILE) not found" && exit 1)
	$(COMPOSE) stop api probe
	@# api and probe come back whether or not the restore worked.
	@$(COMPOSE) exec -T db sh -c 'pg_restore -U "$$POSTGRES_USER" -d "$$POSTGRES_DB" --clean --if-exists --no-owner' < "$(FILE)"; \
		status=$$?; \
		$(COMPOSE) start api probe; \
		test $$status -eq 0 || echo "restore: pg_restore exited $$status; check the output above"; \
		exit $$status

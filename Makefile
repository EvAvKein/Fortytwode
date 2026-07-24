# fortytwode — common dev tasks. Run `make <target>`.
#
# A .env in this directory is loaded automatically and its vars exported to the
# run targets (so DATABASE_URL, FT_CLIENT_ID, etc. reach the binary). Use the
# .env.example format: plain KEY=value, no quotes. Exporting in your shell still
# works too.
ifneq (,$(wildcard .env))
include .env
export
endif

BINARY := fortytwode
.DEFAULT_GOAL := build

# ============================================================================
# ║ DEV
# ============================================================================

# setup-hooks: install the pre-push hook into .git/hooks/
setup-hooks:
	cp pre-push .git/hooks/pre-push
	chmod +x .git/hooks/pre-push

# dev: build + run the full stack (db, app, HTTP Nginx) at http://localhost:8080,
# rebuilds the app on source edits
dev:
	docker compose up --build --watch

# ============================================================================
# ║ CODEGEN
# ============================================================================

# build: regenerate templ code, then compile the binary (default target)
build: generate
	go build -o $(BINARY) .

# generate: regenerate the templ view code
generate:
	go generate ./...

# clean: remove the binary and generated templ code
clean:
	rm -f $(BINARY)
	find internal -name '*_templ.go' -delete

# ============================================================================
# ║ DOCKER LIFECYCLE
# ============================================================================

# logs: follow the containers' logs
logs:
	docker compose logs -f

# down: stop the containers
down:
	docker compose down

# prune: remove all containers and locally-built images that belong to this project
prune:
	docker compose down --rmi local

# volume-rm: remove the database volume (run after `make down` to ensure containers are stopped)
volume-rm:
	docker volume rm fortytwode_pgdata

# ============================================================================
# ║ TESTS & CODE QUALITY
# ============================================================================

# test: run the Go test suite.
# When the local Postgres isn't already running, spins it up and tears it down afterwards
test: generate
	@db_started=0; \
	if ! nc -z localhost 5432 2>/dev/null; then \
		docker compose up -d db --wait; \
		db_started=1; \
	fi; \
	go test ./...; \
	if [ "$$db_started" = "1" ]; then docker compose down; fi

# check: format, vet, scan for known CVEs, and build
check: fmt-check vet vuln build

# fmt: format Go and templ source
fmt:
	gofmt -w .
	go tool templ fmt .

# fmt-check: fail if any Go file is not gofmt-clean (used by `check`/CI).
# Runs gofmt -w first to auto-fix, then -l to fail on anything that remained dirty
# (e.g. a file that couldn't be parsed).
fmt-check: fmt
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "gofmt needs running on:"; echo "$$out"; exit 1; fi

# vet: catch likely bugs the compiler accepts
# e.g bad Printf args, unreachable code, misused struct tags
vet: generate
	go vet ./...

# vuln: scan dependencies and reachable code for known CVEs
vuln:
	go tool govulncheck ./...

# tidy: prune go.mod / go.sum
tidy:
	go mod tidy

# ============================================================================
# ║ CLI TOOLS
# ============================================================================

# fetch: build, then download your own 42 data into ./output (CLI personal tool)
fetch: build
	./$(BINARY) fetch

# fetch-curated: build, then dump only the single curated JSON we'd persist (./output/curated.json).
# Serves as a transparency preview of what the database stores
fetch-curated: build
	./$(BINARY) fetch curated

# ============================================================================
# ║ PRODUCTION
# ============================================================================

# deploy: full prod deploy/renewal
deploy: down cert prod

# prod: (re)start the production stack (TLS + rate-limiting Nginx) on :80/:443, detached.
# Assumes a cert already exists - use `make deploy` for the first run
prod: cloudflare-ips
	docker compose -f docker-compose.yml -f deploy/docker-compose.prod.yml up --build -d

# cloudflare-ips: regenerate deploy/nginx/cloudflare_realip.conf (gitignored) from
# Cloudflare's published ranges. The prod Nginx trusts CF-Connecting-IP only from
# these, so a failed/garbled fetch must abort the deploy rather than ship an
# empty trust list; a previously generated file survives the failure.
CF_REALIP := deploy/nginx/cloudflare_realip.conf
cloudflare-ips:
	@curl -fsS https://www.cloudflare.com/ips-v4 > $(CF_REALIP).tmp4
	@curl -fsS https://www.cloudflare.com/ips-v6 > $(CF_REALIP).tmp6
	@test -s $(CF_REALIP).tmp4 && test -s $(CF_REALIP).tmp6
	@awk 'NF { if ($$0 !~ /^[0-9a-fA-F.:\/]+$$/) { print "unexpected line: " $$0 > "/dev/stderr"; exit 1 } print "set_real_ip_from " $$0 ";" }' \
		$(CF_REALIP).tmp4 $(CF_REALIP).tmp6 > $(CF_REALIP).tmp
	@mv $(CF_REALIP).tmp $(CF_REALIP)
	@rm -f $(CF_REALIP).tmp4 $(CF_REALIP).tmp6
	@echo "wrote $(CF_REALIP) ($$(wc -l < $(CF_REALIP)) ranges)"

# update-prod: pull remote updates, migrate the database, and relaunch prod
update-prod: pull migrate prod

# cert: obtain/renew the Let's Encrypt certificate.
# Usually run via `make deploy`, use standalone only when you need to renew the cert without redeploying prod
cert:
	docker compose -f deploy/docker-compose.cert.yml up --force-recreate --exit-code-from certbot
	docker compose -f deploy/docker-compose.cert.yml down

# pull: fetch latest changes from the remote repository
pull:
	git pull

# ============================================================================
# ║ DATABASE
# ============================================================================

# migrate: apply any pending database migrations against the running database.
# `prod` also applies them on boot, this runs them standalone (e.g. before a deploy) without starting the server
migrate:
	docker compose run --build --rm app migrate

# backup: dump the database to ./backups/<timestamp>.dump (Postgres' custom format).
# Restore from a dump with `make restore FILE=<dump>`.
backup:
	@mkdir -p backups
	docker compose exec -T db pg_dump -U fortytwode -Fc fortytwode > backups/$$(date +%F-%H%M).dump

# restore: load a Postgres dump into the database, replacing current data.
# Usage: make restore FILE=backups/2026-07-02-1200.dump
restore:
	@test -n "$(FILE)" || { echo "usage: make restore FILE=<dump>" >&2; exit 1; }
	docker compose exec -T db pg_restore -U fortytwode -d fortytwode --clean < $(FILE)

# schema: regenerate the reference internal/store/schema.sql from the running db.
# Generated file is for reference only. The source of truth is in the internal/store/migrations/*.sql files
schema:
	@echo "Regenerating schema..."
	@bash -o pipefail -c '{ echo "-- REFERENCE ONLY — generated by '\''make schema'\'', NOT executed. The source"; \
	  echo "-- of truth is internal/store/migrations/*.sql, applied by store.Migrate."; \
	  echo; \
	  docker compose exec -T db pg_dump --schema-only --no-owner -U fortytwode fortytwode 2>/dev/null; } \
	  | docker run --rm -i -v "$$(pwd):/src" -w /src golang:1.26-alpine go run ./internal/store/schema_prettify 2>/dev/null \
	  > internal/store/schema.sql' \
	  && echo "Schema written to internal/store/schema.sql" \
	  || { echo "Failed to regenerate schema" >&2; exit 1; }

.PHONY: setup-hooks dev build generate clean logs down prune volume-rm test check fmt fmt-check vet vuln tidy fetch fetch-curated deploy prod cloudflare-ips update-prod cert pull migrate backup restore schema

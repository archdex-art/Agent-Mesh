# Cross-service build/test/lint orchestration (Technical Roadmap.md §8:
# "a single top-level Makefile/Taskfile orchestrating per-service builds
# rather than adopting a heavyweight monorepo build system at this scale").
#
# GOWORKPKGS lists every workspace module that currently has real code —
# updated as each milestone ships an implementation, not pre-added as
# no-ops for services that are still empty scaffolding (e.g. cli/ as of
# Milestone 2). `go test`/`go vet` are invoked with explicit paths (not a
# bare `./...` from the repo root) because `go.work` workspaces reject a
# root-relative `./...` that spans multiple modules with a "directory
# prefix . does not contain modules" error — this is expected multi-module
# workspace behavior, not a build misconfiguration.
GOWORKPKGS := ./shared/... ./services/collector/... ./services/query-api/...

.PHONY: test vet up down logs ps test-integration

test:
	go vet $(GOWORKPKGS) && go test $(GOWORKPKGS) -race -cover

vet:
	go vet $(GOWORKPKGS)

# Integration tests require live infrastructure (`make up` first) and are
# excluded from `make test` by their `//go:build integration` tag —
# Technical Roadmap.md §7's testcontainers-driven suite for the
# writer/authkeys ClickHouse and Postgres paths.
test-integration:
	go test -tags integration ./shared/authkeys/... ./services/collector/internal/writer/... -v

up:
	cd deploy && docker compose -p agentmesh up -d

down:
	cd deploy && docker compose -p agentmesh down

ps:
	cd deploy && docker compose -p agentmesh ps

logs:
	cd deploy && docker compose -p agentmesh logs -f

# ──────────────────────────────────────────────────────────────
#  Makefile — prism developer shortcuts
#  Usage: make <target>
# ──────────────────────────────────────────────────────────────

.PHONY: help up down logs build test unit integration \
        ollama-pull psql redis-cli lint vet clean

# ── Default target ────────────────────────────────────────────
help:
	@echo ""
	@echo "  prism developer commands"
	@echo ""
	@echo "  Infrastructure"
	@echo "    make up           Start all Docker services"
	@echo "    make down         Stop services (keeps volumes)"
	@echo "    make down-v       Stop services AND wipe all volumes"
	@echo "    make logs         Tail logs for cache-proxy"
	@echo "    make build        Build the proxy Docker image"
	@echo ""
	@echo "  Ollama"
	@echo "    make ollama-pull  Pull nomic-embed-text model into Ollama"
	@echo ""
	@echo "  Database"
	@echo "    make psql         Open psql shell in the postgres container"
	@echo ""
	@echo "  Cache"
	@echo "    make redis-cli    Open redis-cli shell"
	@echo ""
	@echo "  Tests"
	@echo "    make test         Run all tests (unit + integration)"
	@echo "    make unit         Run unit tests only"
	@echo "    make integration  Run integration tests (requires Docker)"
	@echo ""
	@echo "  Code quality"
	@echo "    make vet          go vet ./..."
	@echo "    make lint         golangci-lint run ./..."
	@echo "    make clean        Remove build artifacts"
	@echo ""

# ── Infrastructure ────────────────────────────────────────────
up:
	docker compose up -d

down:
	docker compose down

down-v:
	docker compose down -v

logs:
	docker compose logs -f cache-proxy

build:
	docker compose build cache-proxy

# ── Ollama ───────────────────────────────────────────────────
ollama-pull:
	@echo "Pulling nomic-embed-text model into Ollama…"
	docker exec prism-ollama ollama pull nomic-embed-text
	@echo "Done. Test with: curl http://localhost:11434/api/embeddings -d '{\"model\":\"nomic-embed-text\",\"prompt\":\"hello\"}'"

# ── Database ─────────────────────────────────────────────────
psql:
	docker exec -it prism-postgres psql -U cache -d cache

maintenance:
	docker exec -i prism-postgres psql -U cache -d cache < scripts/maintenance.sql


# ── Redis ────────────────────────────────────────────────────
redis-cli:
	docker exec -it prism-redis redis-cli

# ── Tests ────────────────────────────────────────────────────
test: unit integration

unit:
	go test -v -race -count=1 ./tests/unit/... ./internal/...

integration:
	go test -v -race -count=1 -timeout=120s ./tests/integration/...

# ── Code quality ─────────────────────────────────────────────
vet:
	go vet ./...

lint:
	golangci-lint run ./...
  
# ── Clean ─────────────────────────────────────────────────────
clean:
	rm -f cmd/server/server cmd/server/server.exe

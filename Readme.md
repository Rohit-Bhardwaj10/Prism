# 🧠 Prism Proxy

[![Go Version](https://img.shields.io/badge/Go-1.22+-00ADD8?style=for-the-badge&logo=go)](https://go.dev/)
[![Docker](https://img.shields.io/badge/Docker-Enabled-2496ED?style=for-the-badge&logo=docker)](https://www.docker.com/)
[![License](https://img.shields.io/badge/License-MIT-green?style=for-the-badge)](LICENSE)

**A drop-in caching proxy for LLM APIs.** Point your existing OpenAI/Anthropic SDK at it and cut costs by up to 85%. No code changes required beyond changing one URL.

```
Your App  →  prism (this)  →  OpenAI / Anthropic / Groq
               ↓ cache hit? Returns instantly for free
               ↓ cache miss? Calls upstream, stores result for next time
```

---

## ⚡ Quickstart (Self-Hosted)

**Prerequisites:** Docker + Docker Compose installed. That's it.

```bash
# 1. Clone the repo
git clone https://github.com/your-username/prism.git
cd prism

# 2. Configure credentials
cp .env.example .env
# Open .env and set POSTGRES_PASSWORD and JWT_SECRET

# 3. Start the stack
docker-compose up -d
# (Note: On first boot, an init container automatically pulls the embeddings model. This takes ~1 minute depending on your connection.)

# 4. Verify it's running
curl http://localhost:8080/health
# {"status":"ready"}
```

**That's it.** Your proxy is live at `http://localhost:8080`.

---

## 🔌 Integration (Change One Line)

### Python — OpenAI SDK
```python
# Before
from openai import OpenAI
client = OpenAI(api_key="sk-your-key")

# After (change only base_url)
from openai import OpenAI
client = OpenAI(
    api_key="sk-your-key",
    base_url="http://localhost:8080/proxy/openai"
)

# Your existing code works unchanged
response = client.chat.completions.create(
    model="gpt-4o",
    messages=[{"role": "user", "content": "What is machine learning?"}]
)
```

### Python — Anthropic SDK
```python
# Before
from anthropic import Anthropic
client = Anthropic(api_key="sk-ant-your-key")

# After
from anthropic import Anthropic
client = Anthropic(
    api_key="sk-ant-your-key",
    base_url="http://localhost:8080/proxy/anthropic"
)
```

### curl (any language)
```bash
# Supported providers: openai, groq, together, anthropic
curl -X POST http://localhost:8080/proxy/openai \
  -H "Authorization: Bearer sk-your-openai-key" \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-4o","messages":[{"role":"user","content":"Explain REST APIs"}]}'
```

---

## 📊 How to Tell It's Working

### Response headers on every call
```
X-Cache-Hit: true        ← it was a cache hit
X-Cache: L1              ← which cache layer served it (L1/L2a/L2b/backend)
```

### Response body — `x_cache_metadata` field
```json
{
  "choices": [{"message": {"content": "..."}}],
  "x_cache_metadata": {
    "hit": true,
    "source": "L1",
    "latency_ms": 2
  }
}
```

### Live dashboard
Open **http://localhost:4000** in your browser (Grafana).
- Default login: the `GRAFANA_ADMIN_USER` / `GRAFANA_ADMIN_PASSWORD` from your `.env`
- See real-time hit rates, cost savings, and latency per tenant

---

## 🏗️ How It Works — The 4-Layer Cache

Every request flows through these layers fastest-first:

```
Request
  │
  ▼
L0  Intent Normalizer   "What's 2+2?" → "what is 2 + 2"  (typo/phrasing normalizer)
  │
  ▼
L1  In-Memory LRU       Sub-1ms. Hot queries served from Go process memory.
  │ miss
  ▼
L2a Redis               Sub-10ms. Shared exact-match cache across instances.
  │ miss
  ▼
L2b Postgres + pgvector  Semantic search. "Tell me about Paris" matches
  │                       "Information about the capital of France" (same meaning).
  │ miss
  ▼
Backend (OpenAI etc.)   Real API call. Result stored in all layers for next time.
```

**Key insight:** L2b uses vector embeddings (via Ollama locally, no API key needed) to find *semantically similar* past answers — not just exact string matches.

---

## 🔒 Multi-Tenant Isolation

Run one proxy for multiple users/teams. Each tenant's cache is completely isolated.

```bash
# Team A's requests
curl -H "X-Tenant-ID: team-alpha" http://localhost:8080/proxy/openai ...

# Team B — cannot see Team A's cached answers
curl -H "X-Tenant-ID: team-beta" http://localhost:8080/proxy/openai ...
```

If `X-Tenant-ID` is omitted, requests go to the `default` tenant.

---

## 🛠️ Configuration Reference

All configuration is done via the `.env` file. Copy `.env.example` to `.env` and edit.

| Variable | Default | Description |
|---|---|---|
| `POSTGRES_PASSWORD` | *required* | Database password |
| `JWT_SECRET` | *required* | Secret for signing auth tokens |
| `OLLAMA_URL` | `http://ollama:11434` | Embedding service URL |
| `L1_MAX_BYTES` | `134217728` | L1 cache size (128MB) |
| `RATE_LIMIT_RPM` | `1000` | Max requests/min per tenant |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `GRAFANA_ADMIN_PASSWORD` | *required* | Grafana dashboard password |

---

## 📡 API Reference

| Method | Endpoint | Description |
|---|---|---|
| `POST` | `/proxy/openai` | OpenAI Chat Completions proxy |
| `POST` | `/proxy/anthropic` | Anthropic Messages API proxy |
| `POST` | `/proxy/groq` | Groq Chat Completions proxy |
| `POST` | `/proxy/together` | Together AI proxy |
| `POST` | `/cache/query` | Direct cache query (legacy) |
| `GET` | `/health` | Health check |
| `GET` | `/metrics` | Prometheus metrics |
| `GET` | `/analytics/cost-savings` | Per-tenant savings report |

---

## 🛠️ Tech Stack

- **Engine:** Go 1.22+ (single binary, ~15MB Docker image)
- **L1:** Custom in-process LRU cache
- **L2a:** Redis 7.2
- **L2b:** PostgreSQL 16 + `pgvector`
- **Embeddings:** Ollama (`nomic-embed-text`) — runs locally, no API key
- **Observability:** Prometheus + Grafana (pre-configured, auto-provisioned)

---

## 🔧 Useful Commands

```bash
# View logs
docker-compose logs -f cache-proxy

# Restart just the proxy (after code changes)
docker-compose restart cache-proxy

# Check cache hit stats
curl http://localhost:8080/metrics | grep cache_hits

# Wipe everything and start fresh
docker-compose down -v && docker-compose up -d

# Rebuild after code changes
docker-compose up -d --build cache-proxy
```

---

## 📄 License

MIT — free to use, modify, and self-host.

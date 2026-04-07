# 🧠 Semantic Cache Proxy

[![Go Version](https://img.shields.io/badge/Go-1.22+-00ADD8?style=for-the-badge&logo=go)](https://go.dev/)
[![Docker](https://img.shields.io/badge/Docker-Enabled-2496ED?style=for-the-badge&logo=docker)](https://www.docker.com/)
[![Observability](https://img.shields.io/badge/Metrics-Prometheus%20%2B%20Grafana-orange?style=for-the-badge)](http://localhost:3000)

An enterprise-grade, high-performance caching proxy designed specifically for **Large Language Models (LLMs)**. It reduces API costs by up to **85%** and latencies by **98%** by intelligently reusing semantic matches using **time-aware, intent-based policies**.

---

## 🏗️ The Multi-Tier Intelligent Architecture

The proxy operates as a "Smart Gateway" between your application and expensive backends (e.g., OpenAI, Anthropic). It uses a sophisticated **4-Layer Strategy** to balance speed with semantic accuracy.

```mermaid
graph TD
    subgraph Client_Ingress ["1. Ingress & Security"]
        A[Client Request] --> B[JWT Auth & Tenant ID]
        B --> C[Rate Limiter]
        C --> D[L0: Intent Normalizer]
    end

    subgraph Fast_Path ["2. The Fast Path (Exact)"]
        D --> E{L1: Pocket LRU}
        E -- Miss --> F{L2a: Redis Sync}
        E -- Hit (Sub-1ms) --> RET[Return Response]
        F -- Hit (Sub-10ms) --> BF1[Backfill L1]
        BF1 --> RET
    end

    subgraph Intelligent_Path ["3. The Brain (Semantic)"]
        F -- Miss --> G[Ollama: Generate Embedding]
        G --> H{L2b: Vector Search}
        H -- "Match found (>85%)" --> I{Policy Engine}
        I -- "Confidence Accepted" --> BF2[Backfill L1 + L2a]
        BF2 --> RET
        I -- "Expired/Rejected" --> J
    end

    subgraph Backend_Sync ["4. The Source of Truth"]
        H -- "No Match" --> J[Singleflight: Deduplicator]
        J --> K[LLM Backend Call]
        K --> L[Async Write-Through]
        L --> M[Update L1, L2a, L2b]
        M --> RET
    end

    %% Stylings
    style A fill:#fff,stroke:#333,color:#000
    style B fill:#fff,stroke:#333,color:#000
    style C fill:#fff,stroke:#333,color:#000
    style D fill:#fff,stroke:#333,color:#000
    style G fill:#fff,stroke:#333,color:#000
    style J fill:#fff,stroke:#333,color:#000
    style L fill:#fff,stroke:#333,color:#000
    style M fill:#fff,stroke:#333,color:#000
    style RET fill:#e8f5e9,stroke:#2e7d32,color:#1b5e20

    style E fill:#e1f5fe,stroke:#01579b,color:#01579b
    style F fill:#e1f5fe,stroke:#01579b,color:#01579b
    style H fill:#f3e5f5,stroke:#7b1fa2,color:#4a148c
    style I fill:#f3e5f5,stroke:#7b1fa2,color:#4a148c
    style K fill:#fff3e0,stroke:#e65100,color:#bf360c

    %% Subgraph Styling
    style Client_Ingress fill:#fafafa,stroke:#ddd,color:#333
    style Fast_Path fill:#fafafa,stroke:#ddd,color:#333
    style Intelligent_Path fill:#fafafa,stroke:#ddd,color:#333
    style Backend_Sync fill:#fafafa,stroke:#ddd,color:#333


```

---

## 🚀 The Data Flow in Detail

### 1. Ingress & Normalization (The Receptionist)
Every request is first validated for security (**JWT**) and tenant-isolation. The **L0 Normalizer** then cleans the query (e.g., *"What's"* becomes *"What is"*). This ensures that minor typos or punctuation don't cause expensive cache misses.

### 2. The Fast Path (L1 & L2a)
- **L1 (In-Memory):** Checks the local Go LRU cache. It's the fastest path, serving hot queries in **under 1ms**.
- **L2a (Redis):** If L1 misses, we check Redis. This allows multiple proxy instances to share the same "exact-match" cache.

### 3. The Semantic Brain (L2b)
If no exact match exists, we get "Smart." Using **Ollama**, we generate a mathematical representation (Vector) of the question's *meaning*. 
- We search **Postgres (pgvector)** for similar meanings.
- **Example:** *"Tell me about Paris"* matches *"Information about the capital of France"* because they share the same intent.

### 4. The Policy Gatekeeper
Before serving a semantic match, our **Policy Engine** evaluates:
- **Similarity Score:** Is it close enough (e.g., >88%)?
- **Staleness:** Is the answer too old for this specific domain (Medical vs. General)?

### 5. Backend & Write-Through
If the Librarian is stumped, we ask the **LLM**. To save money, we use `singleflight` to ensure that if 100 people ask the same question at once, we only pay for **one** LLM call. The result is then "Written-Through" all cache tiers for future users.

---

## ✨ Key Enterprise Features

- **🛡️ Multi-Tenant Isolation:** Tenant A's private data is never visible to Tenant B, even for identical queries.
- **📊 Operational Transparency:** Real-time Grafana dashboards tracking Net Savings, Cache Hit Ratio (CHR), and P95 Latencies.
- **⚡ Performance Guarantee:** Built-in circuit breakers and rate limiters protect your upstream budget and ensure sub-20ms response times for hits.

---

## 🛠️ Tech Stack

- **Engine:** Go 1.22+
- **Memory:** Custom LRU (L1) & Redis 7.2 (L2a)
- **Vector Brain:** PostgreSQL 16 + `pgvector` (L2b)
- **Embeddings:** Ollama (`nomic-embed-text`)
- **Observability:** Prometheus + Grafana

---

## 🏎️ Running the Stack

```bash
# 1. Start all services (DB, Redis, Metrics, Proxy)
docker-compose up -d

# 2. Pull the embedding model
make ollama-pull

# 3. View the Mission Control
# Grafana: http://localhost:3000 (admin/admin)
```

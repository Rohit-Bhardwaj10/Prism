#!/usr/bin/env python3
"""
real_demo.py — Prism Proxy Real Usecase Demo
=============================================
Scenario: "AI-Powered FAQ Bot for a SaaS Company"
  - Multiple users ask questions about the product (overlapping, rephrased)
  - Prism caches responses and serves semantic matches
  - Shows real latency difference: Backend (1–3s) vs Cache hit (<50ms)
  - Proves semantic matching: "how do i cancel" == "what's the cancellation process"

Requirements:
    pip install openai requests

Usage:
    python scripts/real_demo.py --provider groq --key YOUR_GROQ_API_KEY
    python scripts/real_demo.py --provider openai --key YOUR_OPENAI_API_KEY
    python scripts/real_demo.py --provider together --key YOUR_TOGETHER_API_KEY

Get a FREE Groq key at: https://console.groq.com (free, no credit card)
"""

import argparse
import json
import sys
import time
import requests

# ── ANSI colors ───────────────────────────────────────────────────────────────
RESET  = "\033[0m"
BOLD   = "\033[1m"
GREEN  = "\033[32m"
YELLOW = "\033[33m"
CYAN   = "\033[36m"
RED    = "\033[31m"
DIM    = "\033[2m"
PURPLE = "\033[35m"

def g(s): return f"{GREEN}{s}{RESET}"
def y(s): return f"{YELLOW}{s}{RESET}"
def c(s): return f"{CYAN}{s}{RESET}"
def r(s): return f"{RED}{s}{RESET}"
def b(s): return f"{BOLD}{s}{RESET}"
def d(s): return f"{DIM}{s}{RESET}"
def p(s): return f"{PURPLE}{s}{RESET}"

PRISM_URL  = "http://localhost:8080"
TENANT_ID  = "saas-faq-bot"

# ── Provider → model map ─────────────────────────────────────────────────────
PROVIDER_MODELS = {
    "groq":     "openai/gpt-oss-20b",      # active Groq Developer model (1000 tps)
    "openai":   "gpt-4o-mini",             # cheap
    "together": "meta-llama/Llama-3.2-3B-Instruct-Turbo",
}

# ── Realistic SaaS FAQ questions ─────────────────────────────────────────────
# Groups: semantically similar questions that SHOULD hit the cache after 1st call
QUESTION_GROUPS = [
    {
        "label": "Billing / Cancel",
        "questions": [
            "How do I cancel my subscription?",           # 1st call → backend
            "What is the cancellation process?",          # semantic hit → cache
            "how can i unsubscribe from the service?",    # semantic hit → cache
            "I want to stop my plan, what do I do?",      # semantic hit → cache
        ]
    },
    {
        "label": "Password Reset",
        "questions": [
            "How do I reset my password?",                # 1st call → backend
            "I forgot my password, how do I recover it?", # semantic hit → cache
            "what's the process to change my password?",  # semantic hit → cache
        ]
    },
    {
        "label": "Data Export",
        "questions": [
            "Can I export my data?",                      # 1st call → backend
            "How do I download all my data?",             # semantic hit → cache
            "Is there a way to get a backup of my data?", # semantic hit → cache
        ]
    },
    {
        "label": "Pricing / Plans",
        "questions": [
            "What plans do you offer?",                   # 1st call → backend
            "Tell me about your pricing tiers",           # semantic hit → cache
            "What's the difference between free and pro?",# semantic hit → cache
        ]
    },
]

SYSTEM_PROMPT = (
    "You are a helpful support assistant for AcmeSaaS, a project management tool. "
    "Answer questions concisely in 2-3 sentences. If you don't know exact details, "
    "give a realistic generic answer for a SaaS product."
)

# ── Stats tracking ────────────────────────────────────────────────────────────
stats = {
    "total": 0,
    "hits": 0,
    "misses": 0,
    "l1_hits": 0,
    "l2a_hits": 0,
    "l2b_hits": 0,
    "latencies_hit": [],
    "latencies_miss": [],
    "saved_calls": 0,
}


def call_prism(provider: str, api_key: str, model: str, question: str) -> dict:
    """Send a question through the Prism proxy and return parsed result."""
    url = f"{PRISM_URL}/proxy/{provider}"
    headers = {
        "Authorization": f"Bearer {api_key}",
        "Content-Type": "application/json",
        "X-Tenant-ID": TENANT_ID,
    }
    body = {
        "model": model,
        "messages": [
            {"role": "user", "content": question}
        ],
        "max_tokens": 150,
    }
    if SYSTEM_PROMPT:
        body["messages"].insert(0, {"role": "system", "content": SYSTEM_PROMPT})

    start = time.time()
    resp = requests.post(url, headers=headers, json=body, timeout=60)
    elapsed_ms = (time.time() - start) * 1000

    if resp.status_code != 200:
        raise RuntimeError(f"Prism error {resp.status_code}: {resp.text[:300]}")

    data = resp.json()

    # Extract cache metadata from response headers
    cache_hit   = resp.headers.get("X-Cache-Hit", "false").lower() == "true"
    cache_layer = resp.headers.get("X-Cache", "backend")
    # Also try response body metadata
    meta = data.get("x_cache_metadata", {})
    if meta:
        cache_hit   = meta.get("hit", cache_hit)
        cache_layer = meta.get("source", cache_layer)

    # Extract answer
    answer = ""
    choices = data.get("choices", [])
    if choices:
        answer = choices[0].get("message", {}).get("content", "")

    return {
        "answer": answer,
        "hit": cache_hit,
        "source": cache_layer,
        "latency_ms": elapsed_ms,
    }


def print_result(question: str, result: dict, group_label: str):
    """Pretty-print a single query result."""
    hit     = result["hit"]
    source  = result["source"]
    lat_ms  = result["latency_ms"]
    answer  = result["answer"][:120] + "..." if len(result["answer"]) > 120 else result["answer"]

    # Tier badge
    if not hit:
        badge    = y("🔄 BACKEND CALL")
        src_line = y(f"  ↳ Cache miss → Real LLM API called → Stored in cache")
    elif source == "L1":
        badge    = g("✅ L1 HIT     ")
        src_line = g("  ↳ Served from: In-memory LRU (sub-ms)")
    elif source == "L2a":
        badge    = c("✅ L2a HIT    ")
        src_line = c("  ↳ Served from: Redis (exact match, ~2ms)")
    else:
        badge    = p("✅ L2b HIT    ")
        src_line = p(f"  ↳ Served from: Postgres vector search (semantic match)")

    # Latency colouring
    if lat_ms < 50:
        lat_str = g(f"{lat_ms:.0f}ms")
    elif lat_ms < 300:
        lat_str = c(f"{lat_ms:.0f}ms")
    elif lat_ms < 1500:
        lat_str = y(f"{lat_ms:.0f}ms")
    else:
        lat_str = r(f"{lat_ms:.0f}ms")

    print(f"\n  {badge}  {lat_str}")
    print(src_line)
    print(d(f'  question: "{question}"'))
    if hit and answer:
        print(d(f'  answer  : "{answer}"'))

    # Update stats
    stats["total"] += 1
    if hit:
        stats["hits"] += 1
        stats["latencies_hit"].append(lat_ms)
        if source == "L1":  stats["l1_hits"] += 1
        elif source == "L2a": stats["l2a_hits"] += 1
        else:               stats["l2b_hits"] += 1
    else:
        stats["misses"] += 1
        stats["latencies_miss"].append(lat_ms)


def print_divider(label: str = ""):
    if label:
        pad = (58 - len(label)) // 2
        print(d(f"\n  {'─' * pad} {b(label)} {'─' * pad}"))
    else:
        print(d(f"\n  {'─' * 60}"))


def print_summary():
    total  = stats["total"]
    hits   = stats["hits"]
    misses = stats["misses"]
    rate   = (hits / total * 100) if total else 0

    avg_hit  = sum(stats["latencies_hit"]) / len(stats["latencies_hit"]) if stats["latencies_hit"] else 0
    avg_miss = sum(stats["latencies_miss"]) / len(stats["latencies_miss"]) if stats["latencies_miss"] else 0
    speedup  = avg_miss / avg_hit if avg_hit > 0 else 0

    # Rough cost savings: assume $0.002 per backend call (gpt-4o-mini/groq scale)
    cost_per_call = 0.002
    saved_usd = hits * cost_per_call

    print()
    print(b(c("  ╔══════════════════════════════════════════════════════════╗")))
    print(b(c("  ║")) + b("              PRISM PROXY — SESSION SUMMARY               ") + b(c("║")))
    print(b(c("  ╚══════════════════════════════════════════════════════════╝")))
    print()
    print(f"    {'Total questions asked:':<35} {total}")
    hit_color = g if rate >= 60 else y
    print(f"    {'Cache hits:':<35} {hit_color(str(hits))} ({rate:.1f}%)")
    print(f"      {'↳ L1 (in-memory):':<33} {stats['l1_hits']}")
    print(f"      {'↳ L2a (Redis exact match):':<33} {stats['l2a_hits']}")
    print(f"      {'↳ L2b (Postgres semantic match):':<33} {stats['l2b_hits']}")
    print(f"    {'Backend (LLM API) calls:':<35} {misses}")
    print()
    if avg_miss > 0:
        print(f"    {'Avg latency — cache hit:':<35} {g(f'{avg_hit:.0f}ms')}")
        print(f"    {'Avg latency — backend call:':<35} {r(f'{avg_miss:.0f}ms')}")
        print(f"    {'Speed improvement:':<35} {g(f'{speedup:.1f}x faster')}")
    print()
    print(f"    {'API calls avoided:':<35} {g(str(hits))}")
    print(f"    {'Estimated cost saved:':<35} {g(f'${saved_usd:.4f}')}")
    print(f"    {'Cost without cache ({total} calls):':<35} ${total * cost_per_call:.4f}")
    print(f"    {'Actual cost (only {misses} backend calls):':<35} ${misses * cost_per_call:.4f}")
    print()

    # Key insight
    print(d("  ──────────────────────────────────────────────────────────"))
    print(f"  {b('Key Insight:')} {stats['l2b_hits']} response(s) were served by SEMANTIC MATCHING —")
    print(f"  the question was phrased differently but Prism recognized")
    print(f"  the same intent and returned a cached answer instantly.")
    print(d("  ──────────────────────────────────────────────────────────"))
    print()


def run_demo(provider: str, api_key: str):
    model = PROVIDER_MODELS[provider]

    print()
    print(b(c("  ╔══════════════════════════════════════════════════════════╗")))
    print(b(c("  ║")) + b("       PRISM PROXY — REAL USECASE DEMO (SaaS FAQ Bot)     ") + b(c("║")))
    print(b(c("  ╚══════════════════════════════════════════════════════════╝")))
    print()
    print(f"  Provider : {b(provider)} ({model})")
    print(f"  Proxy    : {b(PRISM_URL)}")
    print(f"  Tenant   : {b(TENANT_ID)}")
    print()
    print(f"  {d('Scenario: Multiple support team members asking overlapping FAQ questions.')}")
    print(f"  {d('Watch how repeated/rephrased questions get served from cache instantly.')}")
    print(d("  ──────────────────────────────────────────────────────────"))

    for group in QUESTION_GROUPS:
        print_divider(f"Category: {group['label']}")
        for i, question in enumerate(group["questions"]):
            if i == 0:
                print(f"\n  {d('[First ask → will call real LLM API]')}")
            else:
                print(f"\n  {d('[Rephrased → should hit semantic cache]')}")

            try:
                result = call_prism(provider, api_key, model, question)
                print_result(question, result, group["label"])
            except Exception as e:
                print(f"\n  {r('ERROR:')} {e}")
                print(f"  {d('Skipping this question...')}")
                stats["total"] += 1
                stats["misses"] += 1

            # Small pause so it's readable during presentation
            time.sleep(0.3)

    print_summary()


def main():
    parser = argparse.ArgumentParser(
        description="Prism Proxy — Real SaaS FAQ Bot Demo",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
Examples:
  python scripts/real_demo.py --provider groq --key gsk_...
  python scripts/real_demo.py --provider openai --key sk-...
  python scripts/real_demo.py --provider together --key ...

Get a FREE Groq key (no credit card): https://console.groq.com
        """
    )
    parser.add_argument("--provider", choices=["groq", "openai", "together"], default="groq",
                        help="LLM provider (default: groq)")
    parser.add_argument("--key", required=True, help="API key for the chosen provider")
    parser.add_argument("--url", default="http://localhost:8080",
                        help="Prism proxy URL (default: http://localhost:8080)")
    args = parser.parse_args()

    global PRISM_URL
    PRISM_URL = args.url

    # Quick health check
    try:
        health = requests.get(f"{PRISM_URL}/health", timeout=5).json()
        if health.get("status") != "ready":
            print(r(f"❌ Prism proxy is NOT ready: {health}"))
            sys.exit(1)
        print(g(f"✅ Prism proxy is ready at {PRISM_URL}"))
    except Exception as e:
        print(r(f"❌ Cannot reach Prism proxy at {PRISM_URL}: {e}"))
        print(d("   Make sure docker-compose is running: docker-compose up -d"))
        sys.exit(1)

    run_demo(args.provider, args.key)


if __name__ == "__main__":
    main()

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"

	backendpkg "github.com/Rohit-Bhardwaj10/prism/internal/backend"
	"github.com/Rohit-Bhardwaj10/prism/internal/cache"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)


// Handler handles HTTP requests for the semantic cache API.
type Handler struct {
	coord    *cache.Coordinator
	lgMu     sync.Mutex
	lgCmd    *exec.Cmd
}

func NewHandler(coord *cache.Coordinator) *Handler {
	return &Handler{coord: coord}
}

// HandleQuery processes a POST /cache/query request.
func (h *Handler) HandleQuery(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req QueryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid body", http.StatusBadRequest)
		return
	}

	if err := req.Validate(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Delegate to coordinator
	// Extract tenant_id injected by AuthMiddleware
	tenantID := GetTenantID(r.Context())
	requestID := GetRequestID(r.Context())

	qReq := cache.QueryRequest{
		Query:     req.Query,
		TenantID:  tenantID,
		Domain:    req.Domain,
		RequestID: requestID,
	}

	start := time.Now()
	res, err := h.coord.Query(r.Context(), qReq)
	if err != nil {
		http.Error(w, "Internal server error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Prepare API response
	resp := QueryResponse{
		Answer:     res.Answer,
		Source:     res.Source,
		Hit:        res.Hit,
		Confidence: res.Confidence,
		LatencyMS:  time.Since(start).Milliseconds(),
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// HandleHealth processes GET /health (shallow) or /readyz (deep) requests.
func (h *Handler) HandleHealth(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/health" || r.URL.Path == "/livez" {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(HealthResponse{Status: "ready"})
		return
	}

	// Deep check for /readyz
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	status, ready := h.coord.CheckHealth(ctx)
	
	w.Header().Set("Content-Type", "application/json")
	if !ready {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	_ = json.NewEncoder(w).Encode(status)
}


// HandleAnalytics processes a GET /analytics/cost-savings request.
func (h *Handler) HandleAnalytics(w http.ResponseWriter, r *http.Request) {
	tenantID := GetTenantID(r.Context())
	var stats map[string]interface{}
	var err error
	if h.coord != nil {
		stats, err = h.coord.GetStats(r.Context(), tenantID)
	} else {
		stats = map[string]interface{}{"tenant_id": tenantID}
	}
	if err != nil {
		http.Error(w, "Failed to retrieve analytics: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(stats)
}

// HandleAdminInvalidate processes a POST /admin/invalidate request.
// It accepts a tenant_id + normalized query / query_hash and invalidates all cache layers.
func (h *Handler) HandleAdminInvalidate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TenantID        string `json:"tenant_id"`
		NormalizedQuery string `json:"query_normalized"`
		QueryHash       string `json:"query_hash"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid body", http.StatusBadRequest)
		return
	}
	if req.TenantID == "" {
		req.TenantID = GetTenantID(r.Context())
	}

	if h.coord != nil {
		if err := h.coord.Invalidate(r.Context(), req.TenantID, req.QueryHash, req.NormalizedQuery); err != nil {
			http.Error(w, "Invalidation failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	w.WriteHeader(http.StatusAccepted)
	fmt.Fprintf(w, "Invalidation completed for tenant %s", req.TenantID)
}

// HandleAdminReload processes a POST /admin/reload-policies request.
func (h *Handler) HandleAdminReload(w http.ResponseWriter, r *http.Request) {
	if err := h.coord.ReloadPolicies(); err != nil {
		http.Error(w, "Failed to reload: "+err.Error(), http.StatusInternalServerError)
		return
	}
	fmt.Fprintln(w, "Policies reloaded successfully")
}

// HandleStreamQuery processes a GET /cache/stream?q=... request using SSE.
func (h *Handler) HandleStreamQuery(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if query == "" {
		http.Error(w, "Query parameter 'q' required", http.StatusBadRequest)
		return
	}

	tenantID := GetTenantID(r.Context())
	requestID := GetRequestID(r.Context())

	// Set headers for SSE
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	// Call coordinator (For MVP streaming, we'll use non-streaming Query and split response)
	qReq := cache.QueryRequest{
		Query:     query,
		TenantID:  tenantID,
		RequestID: requestID,
	}
	
	resp, err := h.coord.Query(r.Context(), qReq)
	if err != nil {
		fmt.Fprintf(w, "event: error\ndata: %s\n\n", err.Error())
		flusher.Flush()
		return
	}

	// Stream back the answer in chunks to simulate streaming
	words := strings.Split(resp.Answer, " ")
	for i, word := range words {
		chunk := word
		if i < len(words)-1 {
			chunk += " "
		}
		
		event := map[string]interface{}{
			"text":   chunk,
			"source": resp.Source,
		}
		data, _ := json.Marshal(event)
		fmt.Fprintf(w, "data: %s\n\n", string(data))
		flusher.Flush()
	}

	fmt.Fprintf(w, "event: done\ndata: [DONE]\n\n")
	flusher.Flush()
}

// HandleFeedback processes a POST /feedback request.
func (h *Handler) HandleFeedback(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RequestID string `json:"request_id"`
		Correct   bool   `json:"correct"`
		Reason    string `json:"reason,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid body", http.StatusBadRequest)
		return
	}

	tenantID := GetTenantID(r.Context())
	if h.coord != nil {
		_ = h.coord.RecordFeedback(r.Context(), req.RequestID, tenantID, req.Reason, req.Correct)
	}
	log.Printf("[FEEDBACK] TenantID: %s, RequestID: %s, Correct: %v, Reason: %s\n", tenantID, req.RequestID, req.Correct, req.Reason)

	w.WriteHeader(http.StatusAccepted)
}

// HandleMetrics exposesprometheus metrics.
func (h *Handler) HandleMetrics(w http.ResponseWriter, r *http.Request) {
	promhttp.Handler().ServeHTTP(w, r)
}

// HandleLoadgenStart processes a POST /admin/loadgen/start request.
func (h *Handler) HandleLoadgenStart(w http.ResponseWriter, r *http.Request) {
	h.lgMu.Lock()
	defer h.lgMu.Unlock()

	if h.lgCmd != nil {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status": "already running"}`))
		return
	}

	cmd := exec.Command("go", "run", "./cmd/loadgen/")
	// Assuming it's running in the semantic-cache folder where cmd/loadgen exists
	err := cmd.Start()
	if err != nil {
		http.Error(w, "Failed to start load generator", http.StatusInternalServerError)
		return
	}

	h.lgCmd = cmd
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status": "started"}`))
}

// HandleLoadgenStop processes a POST /admin/loadgen/stop request.
func (h *Handler) HandleLoadgenStop(w http.ResponseWriter, r *http.Request) {
	h.lgMu.Lock()
	defer h.lgMu.Unlock()

	if h.lgCmd == nil {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status": "not running"}`))
		return
	}

	if err := h.lgCmd.Process.Kill(); err != nil {
		http.Error(w, "Failed to kill load generator", http.StatusInternalServerError)
		return
	}
	h.lgCmd.Process.Wait()
	h.lgCmd = nil

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status": "stopped"}`))
}

// ── Proxy handlers ────────────────────────────────────────────────────────────

// HandleProxyOpenAI handles POST /proxy/openai — forwards to OpenAI Chat Completions.
func (h *Handler) HandleProxyOpenAI(w http.ResponseWriter, r *http.Request) {
	h.handleOpenAICompatibleProxy(w, r, "openai")
}

// HandleProxyGroq handles POST /proxy/groq — forwards to Groq Chat Completions.
func (h *Handler) HandleProxyGroq(w http.ResponseWriter, r *http.Request) {
	h.handleOpenAICompatibleProxy(w, r, "groq")
}

// HandleProxyTogether handles POST /proxy/together — forwards to Together AI.
func (h *Handler) HandleProxyTogether(w http.ResponseWriter, r *http.Request) {
	h.handleOpenAICompatibleProxy(w, r, "together")
}

// handleOpenAICompatibleProxy is the shared implementation for OpenAI-compatible proxy routes.
func (h *Handler) handleOpenAICompatibleProxy(w http.ResponseWriter, r *http.Request, provider string) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req OpenAIProxyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON body", http.StatusBadRequest)
		return
	}
	if err := req.Validate(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	tenantID := GetTenantID(r.Context())
	requestID := GetRequestID(r.Context())
	apiKey := GetProxyAPIKey(r)
	start := time.Now()

	// Convert to canonical backend.Message slice
	msgs := make([]backendpkg.Message, 0, len(req.Messages))
	system := req.System
	for _, m := range req.Messages {
		if m.Role == "system" {
			// Some clients embed system prompt in messages[]; hoist it out.
			if system == "" {
				system = m.Content
			}
			continue
		}
		msgs = append(msgs, backendpkg.Message{Role: m.Role, Content: m.Content})
	}

	var toolsStr string
	if req.Tools != nil {
		if tb, err := json.Marshal(req.Tools); err == nil {
			toolsStr = string(tb)
		}
	}

	coordReq := cache.QueryRequest{
		Provider:    provider,
		Model:       req.Model,
		System:      system,
		Messages:    msgs,
		TenantID:    tenantID,
		RequestID:   requestID,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		MaxTokens:   req.MaxTokens,
		Tools:       toolsStr,
	}

	// Normalize for L2b embedding & temporal check using the last user prompt
	lastUser := lastUserMessage(msgs)
	normalized := h.coord.Normalize(lastUser)
	cacheKey := cache.ComputeCacheKeyExported(coordReq, normalized)
	domain := h.coord.ClassifyDomain(normalized)

	hit, err := h.coord.CheckCache(r.Context(), coordReq, normalized, cacheKey, domain, start)
	if err != nil {
		http.Error(w, "Cache check error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	var content, model string
	var usage backendpkg.Usage

	if hit != nil {
		// Cache hit
		content = hit.Answer
		model = req.Model
		usage = hit.Usage
	} else {
		// Cache miss — record metrics and call upstream
		if m := h.coord.Metrics(); m != nil {
			m.CacheMisses.WithLabelValues(tenantID).Inc()
			m.BackendCalls.WithLabelValues(domain, tenantID).Inc()
			m.BackendCostTotal.Add(cache.CostBackendPerRequest)
		}
		proxyReq := &backendpkg.ProxyRequest{
			Provider:    provider,
			Model:       req.Model,
			System:      system,
			Messages:    msgs,
			TenantID:    tenantID,
			RequestID:   requestID,
			APIKey:      apiKey,
			Temperature: req.Temperature,
			TopP:        req.TopP,
			MaxTokens:   req.MaxTokens,
			Tools:       req.Tools,
			ToolChoice:  req.ToolChoice,
		}
		proxyResp, err := h.coord.CallOpenAICompatible(r.Context(), proxyReq)
		if err != nil {
			http.Error(w, "Upstream error: "+err.Error(), http.StatusBadGateway)
			return
		}
		content = proxyResp.Content
		model = proxyResp.Model
		usage = proxyResp.Usage
		hit = &cache.QueryResponse{Source: "backend", Hit: false}

		// Persist asynchronously
		entry := &cache.CacheEntry{
			TenantID:         tenantID,
			QueryRaw:         req.Model + ": " + lastUserMessage(msgs),
			QueryNormalized:  normalized,
			QueryHash:        cacheKey,
			QueryDomain:      domain,
			Answer:           content,
			Provider:         provider,
			Model:            model,
			PromptTokens:     usage.PromptTokens,
			CompletionTokens: usage.CompletionTokens,
			TTLSeconds:       86400,
		}

		var emb []float32
		if embText := cache.EmbeddableTextExported(coordReq, normalized); embText != "" {
			emb, _ = h.coord.EmbedText(r.Context(), embText)
		}
		go h.coord.PersistAsync(context.Background(), tenantID, normalized, cacheKey, domain, entry, emb)
	}

	// Handle streaming mode (OpenAI SSE protocol)
	if req.Stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Cache", hit.Source)
		if hit.Hit {
			w.Header().Set("X-Cache-Hit", "true")
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
			return
		}

		chunkID := "chatcmpl-" + requestID
		words := strings.Split(content, " ")
		for i, word := range words {
			token := word
			if i < len(words)-1 {
				token += " "
			}
			chunk := map[string]interface{}{
				"id":      chunkID,
				"object":  "chat.completion.chunk",
				"created": time.Now().Unix(),
				"model":   model,
				"choices": []map[string]interface{}{
					{
						"index": 0,
						"delta": map[string]string{
							"content": token,
						},
						"finish_reason": nil,
					},
				},
			}
			data, _ := json.Marshal(chunk)
			fmt.Fprintf(w, "data: %s\n\n", string(data))
			flusher.Flush()
		}

		finalChunk := map[string]interface{}{
			"id":      chunkID,
			"object":  "chat.completion.chunk",
			"created": time.Now().Unix(),
			"model":   model,
			"choices": []map[string]interface{}{
				{
					"index":         0,
					"delta":         map[string]string{},
					"finish_reason": "stop",
				},
			},
		}
		data, _ := json.Marshal(finalChunk)
		fmt.Fprintf(w, "data: %s\n\n", string(data))
		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
		return
	}

	// Build OpenAI-compatible response
	resp := OpenAIProxyResponse{
		ID:     "chatcmpl-cache-" + requestID,
		Object: "chat.completion",
		Model:  model,
	}
	resp.Choices = []struct {
		Index   int `json:"index"`
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	}{
		{
			Index:        0,
			FinishReason: "stop",
			Message: struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			}{Role: "assistant", Content: content},
		},
	}
	resp.Usage.PromptTokens = usage.PromptTokens
	resp.Usage.CompletionTokens = usage.CompletionTokens
	resp.Usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	resp.Usage.Estimated = usage.Estimated
	resp.CacheMetadata.Hit = hit.Hit
	resp.CacheMetadata.Source = hit.Source
	resp.CacheMetadata.Confidence = hit.Confidence
	resp.CacheMetadata.LatencyMS = time.Since(start).Milliseconds()

	// Surface cache status via response headers for easier client inspection
	w.Header().Set("X-Cache", hit.Source)
	if hit.Hit {
		w.Header().Set("X-Cache-Hit", "true")
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// HandleProxyAnthropic handles POST /proxy/anthropic — forwards to Anthropic Messages API.
func (h *Handler) HandleProxyAnthropic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req AnthropicProxyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON body", http.StatusBadRequest)
		return
	}
	if err := req.Validate(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	tenantID := GetTenantID(r.Context())
	requestID := GetRequestID(r.Context())
	apiKey := GetProxyAPIKey(r)
	start := time.Now()

	msgs := make([]backendpkg.Message, 0, len(req.Messages))
	for _, m := range req.Messages {
		msgs = append(msgs, backendpkg.Message{Role: m.Role, Content: m.Content})
	}

	var toolsStr string
	if req.Tools != nil {
		if tb, err := json.Marshal(req.Tools); err == nil {
			toolsStr = string(tb)
		}
	}

	coordReq := cache.QueryRequest{
		Provider:    "anthropic",
		Model:       req.Model,
		System:      req.System,
		Messages:    msgs,
		TenantID:    tenantID,
		RequestID:   requestID,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		MaxTokens:   req.MaxTokens,
		Tools:       toolsStr,
	}

	lastUser := lastUserMessage(msgs)
	normalized := h.coord.Normalize(lastUser)
	cacheKey := cache.ComputeCacheKeyExported(coordReq, normalized)
	domain := h.coord.ClassifyDomain(normalized)

	hit, err := h.coord.CheckCache(r.Context(), coordReq, normalized, cacheKey, domain, start)
	if err != nil {
		http.Error(w, "Cache check error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	var content, model string
	var usage backendpkg.Usage

	if hit != nil {
		content = hit.Answer
		model = req.Model
		usage = hit.Usage
	} else {
		// Cache miss — record metrics and call upstream
		if m := h.coord.Metrics(); m != nil {
			m.CacheMisses.WithLabelValues(tenantID).Inc()
			m.BackendCalls.WithLabelValues(domain, tenantID).Inc()
			m.BackendCostTotal.Add(cache.CostBackendPerRequest)
		}
		proxyReq := &backendpkg.ProxyRequest{
			Provider:    "anthropic",
			Model:       req.Model,
			System:      req.System,
			Messages:    msgs,
			TenantID:    tenantID,
			RequestID:   requestID,
			APIKey:      apiKey,
			Temperature: req.Temperature,
			TopP:        req.TopP,
			MaxTokens:   req.MaxTokens,
			Tools:       req.Tools,
		}
		proxyResp, err := h.coord.CallAnthropic(r.Context(), proxyReq)
		if err != nil {
			http.Error(w, "Upstream error: "+err.Error(), http.StatusBadGateway)
			return
		}
		content = proxyResp.Content
		model = proxyResp.Model
		usage = proxyResp.Usage
		hit = &cache.QueryResponse{Source: "backend", Hit: false}

		entry := &cache.CacheEntry{
			TenantID:         tenantID,
			QueryRaw:         req.Model + ": " + lastUserMessage(msgs),
			QueryNormalized:  normalized,
			QueryHash:        cacheKey,
			QueryDomain:      domain,
			Answer:           content,
			Provider:         "anthropic",
			Model:            model,
			PromptTokens:     usage.PromptTokens,
			CompletionTokens: usage.CompletionTokens,
			TTLSeconds:       86400,
		}

		var emb []float32
		if embText := cache.EmbeddableTextExported(coordReq, normalized); embText != "" {
			emb, _ = h.coord.EmbedText(r.Context(), embText)
		}
		go h.coord.PersistAsync(context.Background(), tenantID, normalized, cacheKey, domain, entry, emb)
	}

	// Handle streaming mode (Anthropic SSE protocol)
	if req.Stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Cache", hit.Source)
		if hit.Hit {
			w.Header().Set("X-Cache-Hit", "true")
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
			return
		}

		msgID := "msg-cache-" + requestID
		startEvt := map[string]interface{}{
			"type": "message_start",
			"message": map[string]interface{}{
				"id":      msgID,
				"type":    "message",
				"role":    "assistant",
				"model":   model,
				"content": []interface{}{},
				"usage":   map[string]int{"input_tokens": usage.PromptTokens, "output_tokens": 0},
			},
		}
		b, _ := json.Marshal(startEvt)
		fmt.Fprintf(w, "event: message_start\ndata: %s\n\n", string(b))

		blockStart := map[string]interface{}{
			"type":          "content_block_start",
			"index":         0,
			"content_block": map[string]string{"type": "text", "text": ""},
		}
		b, _ = json.Marshal(blockStart)
		fmt.Fprintf(w, "event: content_block_start\ndata: %s\n\n", string(b))

		words := strings.Split(content, " ")
		for i, word := range words {
			token := word
			if i < len(words)-1 {
				token += " "
			}
			deltaEvt := map[string]interface{}{
				"type":  "content_block_delta",
				"index": 0,
				"delta": map[string]string{"type": "text_delta", "text": token},
			}
			b, _ = json.Marshal(deltaEvt)
			fmt.Fprintf(w, "event: content_block_delta\ndata: %s\n\n", string(b))
			flusher.Flush()
		}

		blockStop := map[string]interface{}{"type": "content_block_stop", "index": 0}
		b, _ = json.Marshal(blockStop)
		fmt.Fprintf(w, "event: content_block_stop\ndata: %s\n\n", string(b))

		msgDelta := map[string]interface{}{
			"type":  "message_delta",
			"delta": map[string]string{"stop_reason": "end_turn"},
			"usage": map[string]int{"output_tokens": usage.CompletionTokens},
		}
		b, _ = json.Marshal(msgDelta)
		fmt.Fprintf(w, "event: message_delta\ndata: %s\n\n", string(b))

		msgStop := map[string]interface{}{"type": "message_stop"}
		b, _ = json.Marshal(msgStop)
		fmt.Fprintf(w, "event: message_stop\ndata: %s\n\n", string(b))
		flusher.Flush()
		return
	}

	// Build Anthropic-compatible response
	resp := AnthropicProxyResponse{
		ID:    "msg-cache-" + requestID,
		Type:  "message",
		Role:  "assistant",
		Model: model,
	}
	resp.Content = []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}{{Type: "text", Text: content}}
	resp.Usage.InputTokens = usage.PromptTokens
	resp.Usage.OutputTokens = usage.CompletionTokens
	resp.Usage.Estimated = usage.Estimated
	resp.CacheMetadata.Hit = hit.Hit
	resp.CacheMetadata.Source = hit.Source
	resp.CacheMetadata.Confidence = hit.Confidence
	resp.CacheMetadata.LatencyMS = time.Since(start).Milliseconds()

	w.Header().Set("X-Cache", hit.Source)
	if hit.Hit {
		w.Header().Set("X-Cache-Hit", "true")
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// lastUserMessage returns the content of the last user-role message, for QueryRaw storage.
func lastUserMessage(msgs []backendpkg.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" {
			return msgs[i].Content
		}
	}
	return ""
}


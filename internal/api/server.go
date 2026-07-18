package api

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/Rohit-Bhardwaj10/semantic-cache/internal/cache"
)

// Server defines the HTTP server that hosts the semantic cache API.
type Server struct {
	httpServer *http.Server
	mux        *http.ServeMux
	handler    *Handler
	limiter    *RateLimiter
}

func NewServer(addr string, coord *cache.Coordinator) *Server {
	h := NewHandler(coord)
	mux := http.NewServeMux()

	// 10 requests per second with a burst of 20 - adjustable
	rl := NewRateLimiter(10, 20)

	// Register routes
	mux.HandleFunc("/cache/query", h.HandleQuery)
	mux.HandleFunc("/cache/stream", h.HandleStreamQuery)
	mux.HandleFunc("/health", h.HandleHealth)
	mux.HandleFunc("/readyz", h.HandleHealth)
	mux.HandleFunc("/livez", h.HandleHealth)
	mux.HandleFunc("/analytics/cost-savings", h.HandleAnalytics)
	mux.HandleFunc("/feedback", h.HandleFeedback)
	mux.HandleFunc("/metrics", h.HandleMetrics)

	// Proxy routes — drop-in replacements for upstream provider endpoints.
	// Clients set Authorization: Bearer <provider-api-key> and X-Tenant-ID: <tenant>.
	mux.HandleFunc("/proxy/openai", h.HandleProxyOpenAI)
	mux.HandleFunc("/proxy/groq", h.HandleProxyGroq)
	mux.HandleFunc("/proxy/together", h.HandleProxyTogether)
	mux.HandleFunc("/proxy/anthropic", h.HandleProxyAnthropic)

	// Admin routes
	mux.HandleFunc("/admin/invalidate", h.HandleAdminInvalidate)
	mux.HandleFunc("/admin/reload-policies", h.HandleAdminReload)
	mux.HandleFunc("/admin/loadgen/start", h.HandleLoadgenStart)
	mux.HandleFunc("/admin/loadgen/stop", h.HandleLoadgenStop)

	// Wrap in middleware chain (Outer to Inner: CORS -> ReqID -> Logger -> Auth -> RateLimit -> Mux)
	var finalHandler http.Handler = mux
	finalHandler = rl.RateLimitMiddleware(finalHandler)
	finalHandler = AuthMiddleware(finalHandler)
	finalHandler = LoggerMiddleware(finalHandler)
	finalHandler = RequestIDMiddleware(finalHandler)
	finalHandler = CORSMiddleware(finalHandler)

	srv := &http.Server{
		Addr:         addr,
		Handler:      finalHandler,
		IdleTimeout:  time.Minute,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	return &Server{
		httpServer: srv,
		mux:        mux,
		handler:    h,
		limiter:    rl,
	}
}

// ServeHTTP implements the http.Handler interface.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.httpServer.Handler.ServeHTTP(w, r)
}

// Start runs the server until manually stopped or a fatal error occurs.
func (s *Server) Start() error {
	log.Printf("Starting HTTP server on %s", s.httpServer.Addr)
	return s.httpServer.ListenAndServe()
}

// Shutdown initiates a graceful close of the server.
func (s *Server) Shutdown(ctx context.Context) error {
	log.Printf("Shutting down HTTP server...")
	return s.httpServer.Shutdown(ctx)
}

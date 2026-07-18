package api

import (
	"context"
	"crypto/rand"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/time/rate"
)

type contextKey string

const (
	requestIDKey contextKey = "request_id"
	tenantIDKey  contextKey = "tenant_id"
)

// JWT Claims struct
type Claims struct {
	TenantID string `json:"tenant_id"`
	Role     string `json:"role"`
	jwt.RegisteredClaims
}

// jwtKey is loaded once at startup from the JWT_SECRET environment variable.
// The process will not start if the variable is missing or empty.
var jwtKey []byte

func init() {
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		panic("JWT_SECRET environment variable is required but not set. " +
			"Generate one with: openssl rand -hex 32")
	}
	jwtKey = []byte(secret)
}

// RateLimiter manages token buckets per tenant.
type RateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*rate.Limiter
	r       rate.Limit
	b       int
}

func NewRateLimiter(r rate.Limit, b int) *RateLimiter {
	return &RateLimiter{
		buckets: make(map[string]*rate.Limiter),
		r:       r,
		b:       b,
	}
}

func (rl *RateLimiter) GetLimiter(tenantID string) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	limiter, ok := rl.buckets[tenantID]
	if !ok {
		limiter = rate.NewLimiter(rl.r, rl.b)
		rl.buckets[tenantID] = limiter
	}
	return limiter
}

// RequestIDMiddleware injects a unique X-Request-ID into each request context.
func RequestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rid := r.Header.Get("X-Request-ID")
		if rid == "" {
			b := make([]byte, 8)
			_, _ = rand.Read(b)
			rid = fmt.Sprintf("%x", b)
		}

		ctx := context.WithValue(r.Context(), requestIDKey, rid)
		w.Header().Set("X-Request-ID", rid)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// LoggerMiddleware logs every incoming request.
func LoggerMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		
		rid := "unknown"
		if r.Context().Value(requestIDKey) != nil {
			rid = r.Context().Value(requestIDKey).(string)
		}
		
		log.Printf("[%s] %s %s %v\n", rid, r.Method, r.URL.Path, time.Since(start))
	})
}

// GetRequestID retrieves the request ID from a context.
func GetRequestID(ctx context.Context) string {
	if rid, ok := ctx.Value(requestIDKey).(string); ok {
		return rid
	}
	return "unknown"
}

// GetTenantID retrieves the tenant ID from a context.
func GetTenantID(ctx context.Context) string {
	if tid, ok := ctx.Value(tenantIDKey).(string); ok {
		return tid
	}
	return "default"
}

// AuthMiddleware validates JWT and extracts the tenant ID.
// Proxy routes (/proxy/*) bypass JWT validation — they use upstream provider keys directly
// and identify the tenant via X-Tenant-ID header (defaults to "default").
func AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Health checks — no auth required
		if r.URL.Path == "/health" || r.URL.Path == "/readyz" || r.URL.Path == "/livez" {
			next.ServeHTTP(w, r)
			return
		}

		// Proxy routes — read tenant from header, skip JWT
		if strings.HasPrefix(r.URL.Path, "/proxy/") {
			tenantID := r.Header.Get("X-Tenant-ID")
			if tenantID == "" {
				tenantID = "default"
			}
			ctx := context.WithValue(r.Context(), tenantIDKey, tenantID)
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		authHeader := r.Header.Get("Authorization")
		if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
			ctx := context.WithValue(r.Context(), tenantIDKey, "default")
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		tokenString := strings.TrimPrefix(authHeader, "Bearer ")
		claims := &Claims{}

		token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
			return jwtKey, nil
		})

		if err != nil || !token.Valid {
			http.Error(w, "Unauthorized: invalid token", http.StatusUnauthorized)
			return
		}

		ctx := context.WithValue(r.Context(), tenantIDKey, claims.TenantID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RateLimitMiddleware applies per-tenant rate limiting.
func (rl *RateLimiter) RateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tenantID := GetTenantID(r.Context())
		if !rl.GetLimiter(tenantID).Allow() {
			http.Error(w, "Too many requests", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// CORSMiddleware adds CORS headers to the response
func CORSMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS, PUT, DELETE")
		w.Header().Set("Access-Control-Allow-Headers", "Accept, Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, X-Tenant-ID")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// GetProxyAPIKey extracts the raw Bearer token from the Authorization header.
// For proxy routes, this token is the client's upstream provider API key (e.g. an OpenAI sk-...).
// It is NOT validated by our JWT middleware — it is forwarded verbatim to the upstream provider.
// Returns "" if no Authorization: Bearer header is present.
func GetProxyAPIKey(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	return ""
}


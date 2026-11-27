package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func corsRouter(allowed []string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.HandleMethodNotAllowed = true
	r.Use(CORS(allowed))
	// Stands in for auth: the route answers 401 without a token.
	r.GET("/api/v1/order-intents", func(c *gin.Context) {
		if c.GetHeader("Authorization") == "" {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		c.Status(http.StatusOK)
	})
	return r
}

func do(r *gin.Engine, method, origin string, extra map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/api/v1/order-intents", nil)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	for k, v := range extra {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestCORSPreflightFromAllowedOrigin(t *testing.T) {
	r := corsRouter([]string{"http://localhost:5000"})
	w := do(r, http.MethodOptions, "http://localhost:5000", map[string]string{
		"Access-Control-Request-Method":  "POST",
		"Access-Control-Request-Headers": "authorization, idempotency-key",
	})
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", w.Code)
	}
	h := w.Header()
	if got := h.Get("Access-Control-Allow-Origin"); got != "http://localhost:5000" {
		t.Errorf("allow-origin = %q", got)
	}
	if got := h.Get("Access-Control-Allow-Headers"); got != corsAllowHeaders {
		t.Errorf("allow-headers = %q", got)
	}
	if h.Get("Access-Control-Allow-Credentials") != "" {
		t.Error("credentials must never be allowed")
	}
}

func TestCORSActualRequestStillNeedsToken(t *testing.T) {
	r := corsRouter([]string{"http://localhost:5000"})
	w := do(r, http.MethodGet, "http://localhost:5000", nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401: CORS must not bypass auth", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:5000" {
		t.Errorf("allow-origin = %q, want the origin so the browser can read the 401", got)
	}
}

func TestCORSIgnoresOtherOrigins(t *testing.T) {
	for _, tc := range []struct {
		name    string
		allowed []string
		origin  string
	}{
		{"not listed", []string{"http://localhost:5000"}, "https://evil.example"},
		{"port differs", []string{"http://localhost:5000"}, "http://localhost:5001"},
		{"empty list", nil, "http://localhost:5000"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := corsRouter(tc.allowed)
			w := do(r, http.MethodOptions, tc.origin, map[string]string{"Access-Control-Request-Method": "GET"})
			if w.Header().Get("Access-Control-Allow-Origin") != "" {
				t.Error("unlisted origin got an allow-origin header")
			}
			if w.Code == http.StatusNoContent {
				t.Error("unlisted origin got a successful preflight")
			}
		})
	}
}

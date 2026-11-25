package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/trading-bot/bot-engine/internal/infrastructure/http/apierror"
	"github.com/trading-bot/bot-engine/internal/infrastructure/reqid"
)

func TestRecoveryAnswersWithEnvelopeAndRequestID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RequestID(), Recovery(zap.NewNop()))
	r.GET("/boom", func(*gin.Context) { panic("secret internal state") })

	req := httptest.NewRequest(http.MethodGet, "/boom", nil)
	req.Header.Set(reqid.Header, "trace-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", w.Code)
	}
	var b apierror.Body
	if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil {
		t.Fatalf("not JSON: %q", w.Body.String())
	}
	if b.Error != apierror.CodeInternal || b.RequestID != "trace-1" {
		t.Fatalf("envelope = %+v", b)
	}
	if strings.Contains(w.Body.String(), "secret") {
		t.Fatal("panic value leaked to the client")
	}
}

func TestRequestIDReachesHandlerContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RequestID())
	var seen string
	r.GET("/", func(c *gin.Context) { seen = reqid.FromContext(c.Request.Context()) })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if seen == "" || seen != w.Header().Get(reqid.Header) {
		t.Fatalf("context id %q, header %q", seen, w.Header().Get(reqid.Header))
	}
}

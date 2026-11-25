package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/trading-bot/bot-engine/internal/infrastructure/http/apierror"
)

// errRawBroker mimics an upstream error that must never reach a client.
var errRawBroker = errors.New(`alpaca: 403 {"message":"forbidden","account":"PA3XYZ","key":"PKSECRET"}`)

func decodeErr(t *testing.T, w *httptest.ResponseRecorder) apierror.Body {
	t.Helper()
	var b apierror.Body
	if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil {
		t.Fatalf("error body is not the JSON envelope: %q", w.Body.String())
	}
	if b.Error == "" || b.Detail == "" || b.RequestID == "" {
		t.Fatalf("incomplete envelope: %+v", b)
	}
	if got := w.Header().Get("X-Request-ID"); got != b.RequestID {
		t.Fatalf("header X-Request-ID %q != body request_id %q", got, b.RequestID)
	}
	return b
}

func TestBrokerErrorsAreSanitized(t *testing.T) {
	for _, path := range []string{"/api/v1/portfolio/equity", "/api/v1/portfolio/positions"} {
		t.Run(path, func(t *testing.T) {
			e := newEnv(t, false)
			e.broker.fail = errRawBroker
			w := e.do(http.MethodGet, path, agentToken, "")
			if w.Code != http.StatusBadGateway {
				t.Fatalf("status = %d, want 502", w.Code)
			}
			b := decodeErr(t, w)
			if b.Error != apierror.CodeBrokerUnavailable {
				t.Fatalf("code = %q", b.Error)
			}
			for _, leak := range []string{"alpaca", "PA3XYZ", "PKSECRET", "forbidden"} {
				if strings.Contains(w.Body.String(), leak) {
					t.Fatalf("response leaks %q: %s", leak, w.Body.String())
				}
			}
		})
	}
}

func TestInvalidOrderBodyDoesNotEchoDecoderError(t *testing.T) {
	e := newEnv(t, true)
	w := e.do(http.MethodPost, "/api/v1/orders/place", humanToken, `{"symbol": 42}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	b := decodeErr(t, w)
	if b.Error != apierror.CodeInvalidRequest {
		t.Fatalf("code = %q", b.Error)
	}
	for _, leak := range []string{"json:", "Go struct", "placeOrderRequest", "unmarshal"} {
		if strings.Contains(w.Body.String(), leak) {
			t.Fatalf("response leaks decoder detail %q: %s", leak, w.Body.String())
		}
	}
}

func TestAuthAndPolicyErrorsUseEnvelope(t *testing.T) {
	cases := []struct {
		name, method, path, token, body, code string
		status                                int
	}{
		{"no token", http.MethodGet, "/api/v1/trading-mode", "", "", apierror.CodeUnauthorized, 401},
		{"missing scope", http.MethodPost, "/api/v1/orders/place", agentToken, `{}`, apierror.CodeInsufficientScope, 403},
		{"execution disabled", http.MethodPost, "/api/v1/orders/place", humanToken, `{}`, apierror.CodeExecutionDisabled, 403},
		{"unknown route", http.MethodGet, "/api/v1/nope", agentToken, "", apierror.CodeNotFound, 404},
		{"wrong method", http.MethodDelete, "/api/v1/trading-mode", agentToken, "", apierror.CodeMethodNotAllowed, 405},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := newEnv(t, false).do(tc.method, tc.path, tc.token, tc.body)
			if w.Code != tc.status {
				t.Fatalf("status = %d, want %d (%s)", w.Code, tc.status, w.Body.String())
			}
			if b := decodeErr(t, w); b.Error != tc.code {
				t.Fatalf("code = %q, want %q", b.Error, tc.code)
			}
		})
	}
}

func TestInsufficientScopeKeepsRequiredScope(t *testing.T) {
	// agent-gateway reads required_scope at the top level of the body.
	w := newEnv(t, false).do(http.MethodPost, "/api/v1/orders/place", agentToken, `{}`)
	if b := decodeErr(t, w); b.RequiredScope != "orders:place-legacy" {
		t.Fatalf("required_scope = %q", b.RequiredScope)
	}
}

func TestRequestIDEchoedWhenSafeAndReplacedWhenNot(t *testing.T) {
	e := newEnv(t, false)
	send := func(id string) string {
		req := httptest.NewRequest(http.MethodGet, "/health/live", nil)
		req.Header.Set("X-Request-ID", id)
		w := httptest.NewRecorder()
		e.router.ServeHTTP(w, req)
		return w.Header().Get("X-Request-ID")
	}
	if got := send("agent-42.call_7"); got != "agent-42.call_7" {
		t.Fatalf("safe id not echoed: %q", got)
	}
	for _, bad := range []string{"x\ny", strings.Repeat("a", 65), "id with spaces", `"><script>`} {
		got := send(bad)
		if got == bad || got == "" || strings.ContainsAny(got, " \n\"<>") {
			t.Fatalf("unsafe id %q not replaced: %q", bad, got)
		}
	}
}

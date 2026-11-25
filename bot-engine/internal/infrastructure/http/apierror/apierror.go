// Package apierror writes the single error envelope used by every bot-engine
// route (BL-20):
//
//	{"error": "<code>", "detail": "<safe message>", "request_id": "<id>"}
//
// detail is always a fixed, human-readable string chosen by the handler. Raw
// errors (broker responses, Go decoder messages) are logged, never returned.
package apierror

import (
	"github.com/gin-gonic/gin"

	"github.com/trading-bot/bot-engine/internal/infrastructure/reqid"
)

// Error codes. Clients branch on these; never rename one.
const (
	CodeUnauthorized      = "unauthorized"
	CodeInsufficientScope = "insufficient_scope"
	CodeExecutionDisabled = "execution_disabled"
	CodeInvalidRequest    = "invalid_request"
	CodeBrokerUnavailable = "broker_unavailable"
	CodeNotFound          = "not_found"
	CodeMethodNotAllowed  = "method_not_allowed"
	CodeInternal          = "internal_error"
)

// Body is the error envelope.
type Body struct {
	Error         string `json:"error"`
	Detail        string `json:"detail"`
	RequestID     string `json:"request_id,omitempty"`
	RequiredScope string `json:"required_scope,omitempty"`
}

// Abort writes the envelope with status and stops the handler chain.
func Abort(c *gin.Context, status int, code, detail string) {
	AbortWith(c, status, Body{Error: code, Detail: detail})
}

// AbortWith writes b (request_id is filled in) and stops the handler chain.
func AbortWith(c *gin.Context, status int, b Body) {
	b.RequestID = c.GetString(reqid.GinKey)
	c.AbortWithStatusJSON(status, b)
}

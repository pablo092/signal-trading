// Package reqid carries the request (correlation) ID across HTTP handlers, logs
// and outbound calls (BL-20). The same ID is returned to the caller in the
// X-Request-ID header and in every error body.
package reqid

import (
	"context"
	"regexp"

	"github.com/google/uuid"
)

// Header is the HTTP header that carries the ID in both directions.
const Header = "X-Request-ID"

// GinKey is the gin.Context key holding the ID.
const GinKey = "request_id"

type ctxKey struct{}

// valid bounds caller-supplied IDs: short, log-safe characters only.
var valid = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// FromHeader returns the caller's ID when it is log-safe, otherwise a new UUID.
func FromHeader(v string) string {
	if valid.MatchString(v) {
		return v
	}
	return uuid.NewString()
}

// WithContext stores id in ctx.
func WithContext(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// FromContext returns the ID stored in ctx, or "".
func FromContext(ctx context.Context) string {
	id, _ := ctx.Value(ctxKey{}).(string)
	return id
}

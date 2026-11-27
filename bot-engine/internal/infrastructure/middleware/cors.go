package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/trading-bot/bot-engine/internal/infrastructure/reqid"
)

// corsAllowHeaders are the request headers a browser client may send.
const corsAllowHeaders = "Authorization, Content-Type, Idempotency-Key, " + reqid.Header

// CORS lets the listed browser origins call the API (CORS_ALLOWED_ORIGINS).
// Origins must match exactly; there is no wildcard. With an empty list the
// middleware adds nothing, so browsers keep blocking cross-origin calls.
//
// Callers authenticate with a bearer token, never cookies, so credentials are
// not allowed. A preflight (OPTIONS with Access-Control-Request-Method) from an
// allowed origin is answered here with 204, before auth: browsers never send
// the Authorization header on a preflight. Any other request continues to the
// route, which still requires its token.
func CORS(allowed []string) gin.HandlerFunc {
	set := make(map[string]bool, len(allowed))
	for _, o := range allowed {
		set[strings.TrimRight(o, "/")] = true
	}
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin == "" || !set[origin] {
			c.Next()
			return
		}
		h := c.Writer.Header()
		h.Add("Vary", "Origin")
		h.Set("Access-Control-Allow-Origin", origin)
		h.Set("Access-Control-Expose-Headers", reqid.Header)

		if c.Request.Method == http.MethodOptions && c.GetHeader("Access-Control-Request-Method") != "" {
			h.Add("Vary", "Access-Control-Request-Method")
			h.Add("Vary", "Access-Control-Request-Headers")
			h.Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			h.Set("Access-Control-Allow-Headers", corsAllowHeaders)
			h.Set("Access-Control-Max-Age", "600")
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

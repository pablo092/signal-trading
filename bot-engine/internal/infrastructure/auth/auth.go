// Package auth implements scoped static service tokens (ADR-003, M1).
//
// Tokens are never stored in plaintext: the table holds the SHA-256 hex digest of
// each token. Requests present the token as "Authorization: Bearer <token>".
// Deny by default: with an empty table every protected route returns 401.
package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/trading-bot/bot-engine/internal/domain/ports"
	"github.com/trading-bot/bot-engine/internal/infrastructure/http/apierror"
	"github.com/trading-bot/bot-engine/internal/infrastructure/reqid"
)

// Scopes (ADR-003).
const (
	ScopeMarketRead      = "market:read"
	ScopePortfolioRead   = "portfolio:read"
	ScopeIntentCreate    = "intent:create"
	ScopeIntentApprove   = "intent:approve"
	ScopeAdminKillswitch = "admin:killswitch"
	// ScopeOrdersPlaceLegacy guards the deprecated direct /orders/place route.
	// It is human-only and is removed together with that route in M3 (BL-21).
	ScopeOrdersPlaceLegacy = "orders:place-legacy"
)

// Actor types.
const (
	ActorAgent   = "agent"
	ActorHuman   = "human"
	ActorService = "service"
)

var knownScopes = map[string]bool{
	ScopeMarketRead: true, ScopePortfolioRead: true, ScopeIntentCreate: true,
	ScopeIntentApprove: true, ScopeAdminKillswitch: true, ScopeOrdersPlaceLegacy: true,
}

// humanOnlyScopes can never be granted to agent tokens (ADR-003 mandatory rules).
var humanOnlyScopes = map[string]bool{
	ScopeIntentApprove: true, ScopeAdminKillswitch: true, ScopeOrdersPlaceLegacy: true,
}

// Principal is the authenticated caller.
type Principal struct {
	ID     string   `json:"id"`
	Type   string   `json:"type"`
	SHA256 string   `json:"sha256"`
	Scopes []string `json:"scopes"`

	digest []byte
}

// HasScope reports whether the principal holds scope.
func (p *Principal) HasScope(scope string) bool {
	for _, s := range p.Scopes {
		if s == scope {
			return true
		}
	}
	return false
}

// Store is an immutable token table.
type Store struct {
	principals []*Principal
}

// HashToken returns the hex SHA-256 digest used in the token table.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// LoadStore builds a Store from inline JSON or a JSON file (inline wins if both set).
// Both empty yields an empty store (everything protected is denied).
func LoadStore(inlineJSON, filePath string) (*Store, error) {
	raw := strings.TrimSpace(inlineJSON)
	if raw == "" && filePath != "" {
		b, err := os.ReadFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("reading AUTH_TOKENS_FILE: %w", err)
		}
		raw = strings.TrimSpace(string(b))
	}
	if raw == "" {
		return &Store{}, nil
	}
	var entries []*Principal
	if err := json.Unmarshal([]byte(raw), &entries); err != nil {
		return nil, fmt.Errorf("token table is not valid JSON")
	}
	return NewStore(entries)
}

// NewStore validates entries and builds a Store.
func NewStore(entries []*Principal) (*Store, error) {
	seenID := map[string]bool{}
	seenHash := map[string]bool{}
	for i, p := range entries {
		if p == nil || p.ID == "" {
			return nil, fmt.Errorf("token entry %d: id is required", i)
		}
		if seenID[p.ID] {
			return nil, fmt.Errorf("token entry %q: duplicate id", p.ID)
		}
		seenID[p.ID] = true
		switch p.Type {
		case ActorAgent, ActorHuman, ActorService:
		default:
			return nil, fmt.Errorf("token entry %q: type must be agent, human or service", p.ID)
		}
		d, err := hex.DecodeString(strings.ToLower(p.SHA256))
		if err != nil || len(d) != sha256.Size {
			return nil, fmt.Errorf("token entry %q: sha256 must be a 64-char hex digest (never a plaintext token)", p.ID)
		}
		if seenHash[string(d)] {
			return nil, fmt.Errorf("token entry %q: duplicate token hash", p.ID)
		}
		seenHash[string(d)] = true
		if len(p.Scopes) == 0 {
			return nil, fmt.Errorf("token entry %q: at least one scope is required", p.ID)
		}
		for _, s := range p.Scopes {
			if !knownScopes[s] {
				return nil, fmt.Errorf("token entry %q: unknown scope %q", p.ID, s)
			}
			if p.Type == ActorAgent && humanOnlyScopes[s] {
				return nil, fmt.Errorf("token entry %q: agent tokens cannot hold scope %q", p.ID, s)
			}
		}
		p.digest = d
	}
	return &Store{principals: entries}, nil
}

// Len returns the number of configured tokens.
func (s *Store) Len() int { return len(s.principals) }

// Authenticate resolves a plaintext token to a principal, or nil.
// Every entry is compared in constant time.
func (s *Store) Authenticate(token string) *Principal {
	if token == "" {
		return nil
	}
	sum := sha256.Sum256([]byte(token))
	var found *Principal
	for _, p := range s.principals {
		if subtle.ConstantTimeCompare(sum[:], p.digest) == 1 {
			found = p
		}
	}
	// Defense in depth: verification also refuses human-only scopes on agents.
	if found != nil && found.Type == ActorAgent {
		for _, sc := range found.Scopes {
			if humanOnlyScopes[sc] {
				return nil
			}
		}
	}
	return found
}

const principalKey = "auth.principal"

// BearerToken extracts the token from the Authorization header.
func BearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	const prefix = "bearer "
	if len(h) > len(prefix) && strings.EqualFold(h[:len(prefix)], prefix) {
		return strings.TrimSpace(h[len(prefix):])
	}
	return ""
}

// RequestToken returns the caller's token: the Authorization bearer token or,
// for a WebSocket upgrade only, the second entry of
// "Sec-WebSocket-Protocol: bearer, <token>" (browsers cannot set headers on a
// WebSocket). Tokens in the query string are never accepted: they leak into logs.
func RequestToken(r *http.Request) string {
	if t := BearerToken(r); t != "" {
		return t
	}
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		return ""
	}
	var protocols []string
	for _, v := range r.Header.Values("Sec-WebSocket-Protocol") {
		for _, p := range strings.Split(v, ",") {
			protocols = append(protocols, strings.TrimSpace(p))
		}
	}
	if len(protocols) == 2 && strings.EqualFold(protocols[0], "bearer") {
		return protocols[1]
	}
	return ""
}

// Require returns middleware that authenticates the caller and requires scope.
// 401 when the token is missing or unknown, 403 when the scope is missing.
// Every decision is logged as an audit line (never the token itself).
func Require(store *Store, scope string, logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		p := store.Authenticate(RequestToken(c.Request))
		if p == nil {
			audit(logger, c, nil, scope, "unauthenticated")
			apierror.Abort(c, http.StatusUnauthorized, apierror.CodeUnauthorized, "missing or invalid bearer token")
			return
		}
		if !p.HasScope(scope) {
			audit(logger, c, p, scope, "forbidden")
			apierror.AbortWith(c, http.StatusForbidden, apierror.Body{
				Error:         apierror.CodeInsufficientScope,
				Detail:        "the token lacks the scope this route requires",
				RequiredScope: scope,
			})
			return
		}
		c.Set(principalKey, p)
		// The actor travels in the request context so audit events (BL-14) can
		// name who caused them without depending on gin.
		c.Request = c.Request.WithContext(ports.WithActor(c.Request.Context(), ports.Actor{ID: p.ID, Type: p.Type}))
		c.Next()
		audit(logger, c, p, scope, "allowed")
	}
}

// PrincipalFrom returns the authenticated principal set by Require, or nil.
func PrincipalFrom(c *gin.Context) *Principal {
	if v, ok := c.Get(principalKey); ok {
		if p, ok := v.(*Principal); ok {
			return p
		}
	}
	return nil
}

func audit(logger *zap.Logger, c *gin.Context, p *Principal, scope, outcome string) {
	actorID, actorType := "", ""
	if p != nil {
		actorID, actorType = p.ID, p.Type
	}
	logger.Info("audit.request",
		zap.String("actor_id", actorID),
		zap.String("actor_type", actorType),
		zap.String("method", c.Request.Method),
		zap.String("route", c.FullPath()),
		zap.String("scope", scope),
		zap.String("outcome", outcome),
		zap.Int("status", c.Writer.Status()),
		zap.String("request_id", c.GetString(reqid.GinKey)),
	)
}

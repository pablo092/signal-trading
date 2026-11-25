package handlers

import (
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/trading-bot/bot-engine/internal/application/usecases"
	"github.com/trading-bot/bot-engine/internal/domain/entities"
	"github.com/trading-bot/bot-engine/internal/domain/ports"
	"github.com/trading-bot/bot-engine/internal/infrastructure/http/apierror"
	"github.com/trading-bot/bot-engine/internal/infrastructure/messaging"
	"github.com/trading-bot/bot-engine/internal/infrastructure/reqid"
)

// IdempotencyKeyHeader is required on intent creation (ADR-005 §5.4).
const IdempotencyKeyHeader = "Idempotency-Key"

// ReplayedHeader marks a response replayed for a known Idempotency-Key.
const ReplayedHeader = "Idempotent-Replayed"

var (
	idempotencyKeyPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{8,128}$`)
	symbolPattern         = regexp.MustCompile(`^[A-Za-z][A-Za-z.]{0,9}$`)
	strategyPattern       = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
)

// IntentsHandler serves /api/v1/order-intents (M3, docs/runbooks/ORDER_INTENTS.md).
type IntentsHandler struct {
	svc    *usecases.IntentService
	logger *zap.Logger
}

// NewIntentsHandler constructs the handler; svc may be nil (routes answer 503).
func NewIntentsHandler(svc *usecases.IntentService, logger *zap.Logger) *IntentsHandler {
	return &IntentsHandler{svc: svc, logger: logger}
}

type createIntentRequest struct {
	Symbol   string `json:"symbol" binding:"required" example:"AAPL"`
	Strategy string `json:"strategy" binding:"required" example:"rsi"`
}

type decisionRequest struct {
	Reason string `json:"reason" example:"checked the chart"`
}

// IntentList is the list response (contracts/schemas/order-intent-list.schema.json).
type IntentList struct {
	Intents []messaging.IntentData `json:"intents"`
}

func (h *IntentsHandler) ready(c *gin.Context) bool {
	if h.svc == nil {
		apierror.Abort(c, http.StatusServiceUnavailable, apierror.CodeInternal, "order intents are not configured")
		return false
	}
	return true
}

// Create godoc
//
//	@Summary      Propose an order (order intent)
//	@Description  The engine fetches the signal, sizes the order and runs every risk check. The intent then waits for a human approval. Nothing is sent to the broker here.
//	@Tags         OrderIntents
//	@Accept       json
//	@Produce      json
//	@Param        Idempotency-Key  header  string               true  "8-128 chars [A-Za-z0-9._:-]"
//	@Param        body             body    createIntentRequest  true  "symbol and strategy"
//	@Success      201  {object}  messaging.IntentData  "pending_approval, or rejected with risk.checks"
//	@Failure      400  {object}  apierror.Body  "idempotency_key_required or invalid_request"
//	@Failure      409  {object}  apierror.Body  "kill_switch_engaged or intent_exists"
//	@Failure      422  {object}  apierror.Body  "idempotency_key_reused"
//	@Failure      429  {object}  apierror.Body  "rate_limited"
//	@Failure      502  {object}  apierror.Body  "signal_unavailable"
//	@Router       /api/v1/order-intents [post]
func (h *IntentsHandler) Create(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	key := c.GetHeader(IdempotencyKeyHeader)
	if !idempotencyKeyPattern.MatchString(key) {
		apierror.Abort(c, http.StatusBadRequest, apierror.CodeIdempotencyKeyRequired, "header Idempotency-Key is required: 8-128 characters [A-Za-z0-9._:-]")
		return
	}
	var req createIntentRequest
	if err := c.ShouldBindJSON(&req); err != nil || !symbolPattern.MatchString(req.Symbol) || !strategyPattern.MatchString(req.Strategy) {
		apierror.Abort(c, http.StatusBadRequest, apierror.CodeInvalidRequest, "body must be JSON with a ticker symbol and a strategy name")
		return
	}
	ctx := c.Request.Context()
	res, err := h.svc.Create(ctx, usecases.CreateIntentInput{
		Actor: ports.ActorFrom(ctx), Symbol: req.Symbol, Strategy: req.Strategy, IdempotencyKey: key,
	})
	if err != nil {
		h.fail(c, "intent.create_failed", err)
		return
	}
	if res.Replayed {
		c.Header(ReplayedHeader, "true")
	}
	c.JSON(http.StatusCreated, messaging.NewIntentData(res.View))
}

// List godoc
//
//	@Summary  List order intents, newest first
//	@Tags     OrderIntents
//	@Produce  json
//	@Param    status  query  string  false  "filter by status"
//	@Param    limit   query  int     false  "1-200, default 50"
//	@Success  200  {object}  IntentList
//	@Router   /api/v1/order-intents [get]
func (h *IntentsHandler) List(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	f := ports.IntentFilter{Limit: 50}
	if s := c.Query("status"); s != "" {
		st := entities.IntentStatus(s)
		valid := false
		for _, known := range entities.IntentStatuses {
			valid = valid || known == st
		}
		if !valid {
			apierror.Abort(c, http.StatusBadRequest, apierror.CodeInvalidRequest, "unknown status")
			return
		}
		f.Status = st
	}
	if l := c.Query("limit"); l != "" {
		n, err := strconv.Atoi(l)
		if err != nil || n < 1 || n > 200 {
			apierror.Abort(c, http.StatusBadRequest, apierror.CodeInvalidRequest, "limit must be an integer in [1, 200]")
			return
		}
		f.Limit = n
	}
	views, err := h.svc.List(c.Request.Context(), f)
	if err != nil {
		h.fail(c, "intent.list_failed", err)
		return
	}
	out := IntentList{Intents: make([]messaging.IntentData, 0, len(views))}
	for _, v := range views {
		out.Intents = append(out.Intents, messaging.NewIntentData(v))
	}
	c.JSON(http.StatusOK, out)
}

// Get godoc
//
//	@Summary  Get one order intent
//	@Tags     OrderIntents
//	@Produce  json
//	@Param    id  path  string  true  "intent id (uuid)"
//	@Success  200  {object}  messaging.IntentData
//	@Failure  404  {object}  apierror.Body
//	@Router   /api/v1/order-intents/{id} [get]
func (h *IntentsHandler) Get(c *gin.Context) {
	id, ok := h.id(c)
	if !ok {
		return
	}
	v, err := h.svc.Get(c.Request.Context(), id)
	if err != nil {
		h.fail(c, "intent.get_failed", err)
		return
	}
	c.JSON(http.StatusOK, messaging.NewIntentData(v))
}

// Approve godoc
//
//	@Summary      Approve an order intent (human only)
//	@Description  Re-runs every risk check and submits the order. 202 means the broker outcome is unknown: do not retry.
//	@Tags         OrderIntents
//	@Accept       json
//	@Produce      json
//	@Param        id    path  string           true   "intent id"
//	@Param        body  body  decisionRequest  false  "optional reason"
//	@Success      200  {object}  messaging.IntentData  "submitted, filled, or rejected with rejection_code"
//	@Success      202  {object}  messaging.IntentData  "outcome unknown; the reconciler resolves it"
//	@Failure      403  {object}  apierror.Body  "execution_disabled or insufficient_scope"
//	@Failure      409  {object}  apierror.Body  "intent_not_pending or intent_expired"
//	@Failure      503  {object}  apierror.Body  "audit_unavailable"
//	@Router       /api/v1/order-intents/{id}/approve [post]
func (h *IntentsHandler) Approve(c *gin.Context) {
	id, ok := h.id(c)
	if !ok {
		return
	}
	reason, ok := h.reason(c, false)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	v, err := h.svc.Approve(ctx, id, ports.ActorFrom(ctx), reason)
	if errors.Is(err, usecases.ErrOutcomeUnknown) && v.Intent != nil {
		h.logger.Warn("intent.outcome_unknown", zap.String("intent_id", id.String()), zap.String("request_id", reqid.FromContext(ctx)))
		c.JSON(http.StatusAccepted, messaging.NewIntentData(v))
		return
	}
	if err != nil {
		h.fail(c, "intent.approve_failed", err)
		return
	}
	c.JSON(http.StatusOK, messaging.NewIntentData(v))
}

// Reject godoc
//
//	@Summary  Reject an order intent (human only)
//	@Tags     OrderIntents
//	@Accept   json
//	@Produce  json
//	@Param    id    path  string           true  "intent id"
//	@Param    body  body  decisionRequest  true  "reason (required)"
//	@Success  200  {object}  messaging.IntentData
//	@Failure  409  {object}  apierror.Body  "intent_not_pending or intent_expired"
//	@Router   /api/v1/order-intents/{id}/reject [post]
func (h *IntentsHandler) Reject(c *gin.Context) {
	id, ok := h.id(c)
	if !ok {
		return
	}
	reason, ok := h.reason(c, true)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	v, err := h.svc.Reject(ctx, id, ports.ActorFrom(ctx), reason)
	if err != nil {
		h.fail(c, "intent.reject_failed", err)
		return
	}
	c.JSON(http.StatusOK, messaging.NewIntentData(v))
}

// Cancel godoc
//
//	@Summary  Withdraw a pending order intent (its creator only)
//	@Tags     OrderIntents
//	@Produce  json
//	@Param    id  path  string  true  "intent id"
//	@Success  200  {object}  messaging.IntentData
//	@Failure  403  {object}  apierror.Body  "not_intent_owner"
//	@Failure  409  {object}  apierror.Body  "intent_not_pending or intent_expired"
//	@Router   /api/v1/order-intents/{id}/cancel [post]
func (h *IntentsHandler) Cancel(c *gin.Context) {
	id, ok := h.id(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	v, err := h.svc.Cancel(ctx, id, ports.ActorFrom(ctx))
	if err != nil {
		h.fail(c, "intent.cancel_failed", err)
		return
	}
	c.JSON(http.StatusOK, messaging.NewIntentData(v))
}

func (h *IntentsHandler) id(c *gin.Context) (uuid.UUID, bool) {
	if !h.ready(c) {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		apierror.Abort(c, http.StatusNotFound, apierror.CodeNotFound, "no such order intent")
		return uuid.Nil, false
	}
	return id, true
}

// reason reads an optional (or required) decision reason of 1-200 characters.
func (h *IntentsHandler) reason(c *gin.Context, required bool) (string, bool) {
	var req decisionRequest
	if c.Request.ContentLength != 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			apierror.Abort(c, http.StatusBadRequest, apierror.CodeInvalidRequest, `body must be JSON: {"reason": "..."}`)
			return "", false
		}
	}
	n := utf8.RuneCountInString(req.Reason)
	if n > 200 || (required && n == 0) {
		apierror.Abort(c, http.StatusBadRequest, apierror.CodeInvalidRequest, "reason must be 1-200 characters")
		return "", false
	}
	return req.Reason, true
}

// fail maps a service error to the envelope. Raw errors are logged, never returned.
func (h *IntentsHandler) fail(c *gin.Context, event string, err error) {
	status, code, detail := intentError(err)
	if status >= 500 {
		h.logger.Error(event, zap.Error(err), zap.String("request_id", reqid.FromContext(c.Request.Context())))
	} else {
		h.logger.Info(event, zap.String("code", code), zap.String("request_id", reqid.FromContext(c.Request.Context())))
	}
	apierror.Abort(c, status, code, detail)
}

func intentError(err error) (int, string, string) {
	switch {
	case errors.Is(err, ports.ErrIntentNotFound):
		return http.StatusNotFound, apierror.CodeNotFound, "no such order intent"
	case errors.Is(err, ports.ErrKillSwitchEngaged):
		return http.StatusConflict, apierror.CodeKillSwitchEngaged, "trading is halted by the kill switch"
	case errors.Is(err, ports.ErrActiveIntentForSignal):
		return http.StatusConflict, apierror.CodeIntentExists, "an active intent already exists for this signal"
	case errors.Is(err, usecases.ErrIdempotencyKeyReused):
		return http.StatusUnprocessableEntity, apierror.CodeIdempotencyKeyReused, "this Idempotency-Key was used for a different request"
	case errors.Is(err, usecases.ErrIntentRateLimited):
		return http.StatusTooManyRequests, apierror.CodeRateLimited, "too many order intents for this actor; try again later"
	case errors.Is(err, usecases.ErrSignalUnavailable):
		return http.StatusBadGateway, apierror.CodeSignalUnavailable, "the signal service did not return a signal"
	case errors.Is(err, usecases.ErrIntentExpired):
		return http.StatusConflict, apierror.CodeIntentExpired, "the intent expired before a decision"
	case errors.Is(err, usecases.ErrIntentNotPending):
		return http.StatusConflict, apierror.CodeIntentNotPending, "the intent is not waiting for a decision"
	case errors.Is(err, usecases.ErrNotIntentOwner):
		return http.StatusForbidden, apierror.CodeNotIntentOwner, "only the actor that created the intent may cancel it"
	case errors.Is(err, usecases.ErrExecutionDisabled):
		return http.StatusForbidden, apierror.CodeExecutionDisabled, "order execution is disabled on this deployment"
	case errors.Is(err, usecases.ErrAuditUnavailable):
		return http.StatusServiceUnavailable, apierror.CodeAuditUnavailable, "the audit log is unavailable; nothing was sent"
	case errors.Is(err, usecases.ErrInvalidIntent):
		return http.StatusBadRequest, apierror.CodeInvalidRequest, "the proposal is not valid"
	default:
		return http.StatusInternalServerError, apierror.CodeInternal, "the order intent could not be processed"
	}
}

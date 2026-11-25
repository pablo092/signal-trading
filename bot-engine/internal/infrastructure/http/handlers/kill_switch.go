package handlers

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/trading-bot/bot-engine/internal/application/usecases"
	"github.com/trading-bot/bot-engine/internal/infrastructure/http/apierror"
	"github.com/trading-bot/bot-engine/internal/infrastructure/reqid"
)

// KillSwitchHandler serves /api/v1/admin/kill-switch (ADR-005 §5.7, BL-19).
type KillSwitchHandler struct {
	svc    *usecases.KillSwitchService
	logger *zap.Logger
}

// NewKillSwitchHandler constructs the handler; svc may be nil (routes answer 503).
func NewKillSwitchHandler(svc *usecases.KillSwitchService, logger *zap.Logger) *KillSwitchHandler {
	return &KillSwitchHandler{svc: svc, logger: logger}
}

// KillSwitchResponse is the kill-switch state.
type KillSwitchResponse struct {
	Engaged   bool      `json:"engaged"`
	Forced    bool      `json:"forced"`
	Reason    string    `json:"reason"`
	UpdatedBy string    `json:"updated_by"`
	UpdatedAt time.Time `json:"updated_at"`
	// Set on engage only.
	Persisted *bool  `json:"persisted,omitempty"`
	CancelAll string `json:"cancel_all,omitempty"` // "ok" or "failed"
}

type killSwitchRequest struct {
	Engaged *bool  `json:"engaged" binding:"required"`
	Reason  string `json:"reason" binding:"required"`
}

func toResponse(s usecases.KillSwitchStatus) KillSwitchResponse {
	return KillSwitchResponse{Engaged: s.Engaged, Forced: s.Forced, Reason: s.Reason, UpdatedBy: s.UpdatedBy, UpdatedAt: s.UpdatedAt}
}

// Get godoc
//
//	@Summary      Get the kill-switch state
//	@Tags         Admin
//	@Produce      json
//	@Success      200  {object}  KillSwitchResponse
//	@Failure      503  {object}  apierror.Body  "kill_switch_unavailable (state unknown: trading is refused)"
//	@Router       /api/v1/admin/kill-switch [get]
func (h *KillSwitchHandler) Get(c *gin.Context) {
	if h.svc == nil {
		apierror.Abort(c, http.StatusServiceUnavailable, apierror.CodeKillSwitchUnavailable, "the kill switch is not configured")
		return
	}
	st, err := h.svc.Status(c.Request.Context())
	if err != nil {
		h.logger.Error("kill_switch.status_failed", zap.Error(err), zap.String("request_id", reqid.FromContext(c.Request.Context())))
		apierror.Abort(c, http.StatusServiceUnavailable, apierror.CodeKillSwitchUnavailable, "the kill-switch state could not be read; trading is refused until it can")
		return
	}
	c.JSON(http.StatusOK, toResponse(st))
}

// Set godoc
//
//	@Summary      Engage or release the kill switch
//	@Description  Engaging halts every order and cancels open broker orders; it always takes effect. Releasing needs a reason and a successful write, and is refused while KILL_SWITCH=true.
//	@Tags         Admin
//	@Accept       json
//	@Produce      json
//	@Success      200  {object}  KillSwitchResponse
//	@Failure      400  {object}  apierror.Body  "invalid_request"
//	@Failure      409  {object}  apierror.Body  "kill_switch_forced"
//	@Failure      503  {object}  apierror.Body  "kill_switch_unavailable"
//	@Router       /api/v1/admin/kill-switch [post]
func (h *KillSwitchHandler) Set(c *gin.Context) {
	if h.svc == nil {
		apierror.Abort(c, http.StatusServiceUnavailable, apierror.CodeKillSwitchUnavailable, "the kill switch is not configured")
		return
	}
	var req killSwitchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apierror.Abort(c, http.StatusBadRequest, apierror.CodeInvalidRequest, "body must be JSON with a boolean engaged and a string reason")
		return
	}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" || len(reason) > 200 {
		apierror.Abort(c, http.StatusBadRequest, apierror.CodeInvalidRequest, "reason is required (1-200 characters)")
		return
	}
	ctx := c.Request.Context()
	if *req.Engaged {
		res, _ := h.svc.Engage(ctx, reason)
		resp := toResponse(res.Status)
		resp.Persisted = &res.Persisted
		resp.CancelAll = "ok"
		if res.CancelErr != nil {
			resp.CancelAll = "failed"
			h.logger.Error("kill_switch.cancel_all_failed", zap.Error(res.CancelErr), zap.String("request_id", reqid.FromContext(ctx)))
		}
		c.JSON(http.StatusOK, resp)
		return
	}
	st, err := h.svc.Release(ctx, reason)
	switch {
	case errors.Is(err, usecases.ErrKillSwitchForced):
		apierror.Abort(c, http.StatusConflict, apierror.CodeKillSwitchForced, "the kill switch is forced on by the KILL_SWITCH override; redeploy without it to release")
	case err != nil:
		h.logger.Error("kill_switch.release_failed", zap.Error(err), zap.String("request_id", reqid.FromContext(ctx)))
		apierror.Abort(c, http.StatusServiceUnavailable, apierror.CodeKillSwitchUnavailable, "the release could not be saved; the kill switch stays engaged")
	default:
		c.JSON(http.StatusOK, toResponse(st))
	}
}

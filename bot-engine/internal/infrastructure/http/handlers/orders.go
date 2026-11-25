// Package handlers contains Gin HTTP handlers for the bot engine API.
package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/trading-bot/bot-engine/internal/application/usecases"
	"github.com/trading-bot/bot-engine/internal/domain/ports"
	"github.com/trading-bot/bot-engine/internal/infrastructure/http/apierror"
	"github.com/trading-bot/bot-engine/internal/infrastructure/reqid"
)

// OrdersHandler handles order-related HTTP endpoints.
type OrdersHandler struct {
	placeOrder       *usecases.PlaceOrderUseCase
	executionEnabled bool
	logger           *zap.Logger
}

// NewOrdersHandler constructs an OrdersHandler. When executionEnabled is false
// (the default, ADR-005 §5.2) every order route returns 403 execution_disabled.
// Routes are mounted by the server package's route-scope table, never here.
func NewOrdersHandler(placeOrder *usecases.PlaceOrderUseCase, executionEnabled bool, logger *zap.Logger) *OrdersHandler {
	return &OrdersHandler{placeOrder: placeOrder, executionEnabled: executionEnabled, logger: logger}
}

// placeOrderRequest is the JSON body for POST /api/v1/orders/place.
//
// @Description Request to place an order based on a signal.
type placeOrderRequest struct {
	// Symbol is the asset ticker, e.g. "AAPL".
	Symbol string `json:"symbol" binding:"required" example:"AAPL"`
	// Strategy is the signal strategy name, e.g. "rsi".
	Strategy string `json:"strategy" binding:"required" example:"rsi"`
}

// placeOrderResponse is the JSON body returned by POST /api/v1/orders/place.
type placeOrderResponse struct {
	Skipped    bool    `json:"skipped"`
	SkipReason string  `json:"skip_reason,omitempty"`
	OrderID    string  `json:"order_id,omitempty"`
	Symbol     string  `json:"symbol,omitempty"`
	Side       string  `json:"side,omitempty"`
	Quantity   float64 `json:"quantity,omitempty"`
	Status     string  `json:"status,omitempty"`
}

// PlaceOrder godoc
//
//	@Summary      Place an order
//	@Description  Fetches a signal for the given symbol+strategy, applies risk rules, and submits an order to the broker. Returns skipped=true if risk rules reject the signal.
//	@Tags         Orders
//	@Accept       json
//	@Produce      json
//	@Param        body  body      placeOrderRequest   true  "Place order request"
//	@Success      200   {object}  placeOrderResponse
//	@Failure      400   {object}  map[string]string
//	@Failure      401   {object}  map[string]string
//	@Failure      403   {object}  map[string]string  "execution_disabled or insufficient_scope"
//	@Failure      422   {object}  apierror.Body  "order_rejected"
//	@Failure      500   {object}  apierror.Body  "internal_error"
//	@Failure      502   {object}  apierror.Body  "signal_unavailable, broker_unavailable or order_outcome_unknown"
//	@Router       /api/v1/orders/place [post]
func (h *OrdersHandler) PlaceOrder(c *gin.Context) {
	if !h.executionEnabled || h.placeOrder == nil {
		apierror.Abort(c, http.StatusForbidden, apierror.CodeExecutionDisabled, "order execution is disabled on this deployment")
		return
	}

	var req placeOrderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apierror.Abort(c, http.StatusBadRequest, apierror.CodeInvalidRequest, "body must be JSON with string fields symbol and strategy")
		return
	}

	out, err := h.placeOrder.Execute(c.Request.Context(), usecases.PlaceOrderInput{
		Symbol:   req.Symbol,
		Strategy: req.Strategy,
	})
	if err != nil {
		h.logger.Error("place_order.failed", zap.Error(err), zap.String("request_id", reqid.FromContext(c.Request.Context())))
		status, code, detail := placeOrderError(err)
		apierror.Abort(c, status, code, detail)
		return
	}

	resp := placeOrderResponse{
		Skipped:    out.Skipped,
		SkipReason: out.SkipReason,
	}
	if out.Order != nil {
		resp.OrderID = out.Order.ID.String()
		resp.Symbol = out.Order.Symbol
		resp.Side = string(out.Order.Side)
		resp.Quantity = out.Order.Quantity.InexactFloat64()
		resp.Status = string(out.Order.Status)
	}

	c.JSON(http.StatusOK, resp)
}

// placeOrderError maps a use-case error to a status, code and fixed detail.
// Order matters: an unknown outcome must never be reported as a plain failure,
// or a caller might retry and place the order twice.
func placeOrderError(err error) (int, string, string) {
	switch {
	case errors.Is(err, usecases.ErrOutcomeUnknown):
		return http.StatusBadGateway, apierror.CodeOutcomeUnknown,
			"the broker did not confirm the order; it may exist — do not retry, check the order status"
	case errors.Is(err, usecases.ErrSignalUnavailable):
		return http.StatusBadGateway, apierror.CodeSignalUnavailable, "the signal service did not return a signal"
	case errors.Is(err, ports.ErrInsufficientFunds):
		return http.StatusUnprocessableEntity, apierror.CodeOrderRejected, "the broker rejected the order: insufficient buying power"
	case errors.Is(err, ports.ErrRejected), errors.Is(err, ports.ErrInvalidOrder):
		return http.StatusUnprocessableEntity, apierror.CodeOrderRejected, "the broker rejected the order"
	case errors.Is(err, ports.ErrUnavailable), errors.Is(err, ports.ErrRateLimited):
		return http.StatusBadGateway, apierror.CodeBrokerUnavailable, "the broker is unavailable; the order was not placed"
	default:
		return http.StatusInternalServerError, apierror.CodeInternal, "the order could not be processed"
	}
}

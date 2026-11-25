// Package handlers contains Gin HTTP handlers for the bot engine API.
package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/trading-bot/bot-engine/internal/application/usecases"
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
//	@Failure      500   {object}  map[string]string
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
		apierror.Abort(c, http.StatusInternalServerError, apierror.CodeInternal, "the order could not be processed")
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

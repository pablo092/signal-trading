// Package handlers contains Gin HTTP handlers for the bot engine API.
package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/trading-bot/bot-engine/internal/application/usecases"
)

// OrdersHandler handles order-related HTTP endpoints.
type OrdersHandler struct {
	placeOrder *usecases.PlaceOrderUseCase
	logger     *zap.Logger
}

// NewOrdersHandler constructs an OrdersHandler.
func NewOrdersHandler(placeOrder *usecases.PlaceOrderUseCase, logger *zap.Logger) *OrdersHandler {
	return &OrdersHandler{placeOrder: placeOrder, logger: logger}
}

// RegisterRoutes mounts the order routes on the given router group.
func (h *OrdersHandler) RegisterRoutes(rg *gin.RouterGroup) {
	rg.POST("/orders/place", h.PlaceOrder)
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
	Skipped    bool   `json:"skipped"`
	SkipReason string `json:"skip_reason,omitempty"`
	OrderID    string `json:"order_id,omitempty"`
	Symbol     string `json:"symbol,omitempty"`
	Side       string `json:"side,omitempty"`
	Quantity   float64 `json:"quantity,omitempty"`
	Status     string `json:"status,omitempty"`
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
//	@Failure      500   {object}  map[string]string
//	@Router       /api/v1/orders/place [post]
func (h *OrdersHandler) PlaceOrder(c *gin.Context) {
	var req placeOrderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	out, err := h.placeOrder.Execute(c.Request.Context(), usecases.PlaceOrderInput{
		Symbol:   req.Symbol,
		Strategy: req.Strategy,
	})
	if err != nil {
		h.logger.Error("place_order.failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	resp := placeOrderResponse{
		Skipped:    out.Skipped,
		SkipReason: out.SkipReason,
	}
	if out.Order != nil {
		resp.OrderID  = out.Order.ID.String()
		resp.Symbol   = out.Order.Symbol
		resp.Side     = string(out.Order.Side)
		resp.Quantity = out.Order.Quantity
		resp.Status   = string(out.Order.Status)
	}

	c.JSON(http.StatusOK, resp)
}

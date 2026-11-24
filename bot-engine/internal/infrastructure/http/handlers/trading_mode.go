package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// TradingModeInfo is the verified trading posture of the engine.
type TradingModeInfo struct {
	Mode                  string `json:"mode"`
	Verified              bool   `json:"verified"`
	BrokerHost            string `json:"broker_host"`
	OrderExecutionEnabled bool   `json:"order_execution_enabled"`
}

// TradingModeHandler serves GET /api/v1/trading-mode (ADR-005 §5.1).
type TradingModeHandler struct {
	info TradingModeInfo
}

// NewTradingModeHandler constructs a TradingModeHandler from verified config values.
func NewTradingModeHandler(info TradingModeInfo) *TradingModeHandler {
	return &TradingModeHandler{info: info}
}

// Get godoc
//
//	@Summary      Get verified trading mode
//	@Description  Returns the trading mode verified at startup (paper host + PAPER_TRADING) and whether order execution is enabled.
//	@Tags         System
//	@Produce      json
//	@Success      200  {object}  TradingModeInfo
//	@Failure      401  {object}  map[string]string
//	@Router       /api/v1/trading-mode [get]
func (h *TradingModeHandler) Get(c *gin.Context) {
	c.JSON(http.StatusOK, h.info)
}

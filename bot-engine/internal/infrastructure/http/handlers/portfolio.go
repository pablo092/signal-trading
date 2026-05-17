package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/trading-bot/bot-engine/internal/domain/ports"
)

// PortfolioHandler handles portfolio-related HTTP endpoints.
type PortfolioHandler struct {
	broker ports.BrokerPort
	logger *zap.Logger
}

// NewPortfolioHandler constructs a PortfolioHandler.
func NewPortfolioHandler(broker ports.BrokerPort, logger *zap.Logger) *PortfolioHandler {
	return &PortfolioHandler{broker: broker, logger: logger}
}

// RegisterRoutes mounts the portfolio routes on the given router group.
func (h *PortfolioHandler) RegisterRoutes(rg *gin.RouterGroup) {
	rg.GET("/portfolio/equity", h.GetEquity)
	rg.GET("/portfolio/positions", h.GetPositions)
}

// equityResponse is the JSON body for GET /api/v1/portfolio/equity.
type equityResponse struct {
	Equity     float64 `json:"equity"`
	PaperMode  bool    `json:"paper_mode"`
	MarketOpen bool    `json:"market_open"`
}

// GetEquity godoc
//
//	@Summary      Get portfolio equity
//	@Description  Returns the current total account equity and market status from the broker.
//	@Tags         Portfolio
//	@Produce      json
//	@Success      200  {object}  equityResponse
//	@Failure      500  {object}  map[string]string
//	@Router       /api/v1/portfolio/equity [get]
func (h *PortfolioHandler) GetEquity(c *gin.Context) {
	equity, err := h.broker.GetPortfolioEquity(c.Request.Context())
	if err != nil {
		h.logger.Error("portfolio.equity_failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	marketOpen, _ := h.broker.IsMarketOpen(c.Request.Context())

	c.JSON(http.StatusOK, equityResponse{
		Equity:     equity,
		PaperMode:  true,
		MarketOpen: marketOpen,
	})
}

// positionsResponse is the JSON body for GET /api/v1/portfolio/positions.
type positionItem struct {
	Symbol            string  `json:"symbol"`
	Quantity          float64 `json:"quantity"`
	AverageEntryPrice float64 `json:"average_entry_price"`
	CurrentPrice      float64 `json:"current_price"`
	UnrealizedPnL     float64 `json:"unrealized_pnl"`
}

// GetPositions godoc
//
//	@Summary      Get open positions
//	@Description  Returns all currently open positions from the broker.
//	@Tags         Portfolio
//	@Produce      json
//	@Success      200  {array}   positionItem
//	@Failure      500  {object}  map[string]string
//	@Router       /api/v1/portfolio/positions [get]
func (h *PortfolioHandler) GetPositions(c *gin.Context) {
	positions, err := h.broker.GetPositions(c.Request.Context())
	if err != nil {
		h.logger.Error("portfolio.positions_failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	items := make([]positionItem, len(positions))
	for i, p := range positions {
		items[i] = positionItem{
			Symbol:            p.Symbol,
			Quantity:          p.Quantity,
			AverageEntryPrice: p.AverageEntryPrice,
			CurrentPrice:      p.CurrentPrice,
			UnrealizedPnL:     p.UnrealizedPnL,
		}
	}

	c.JSON(http.StatusOK, items)
}

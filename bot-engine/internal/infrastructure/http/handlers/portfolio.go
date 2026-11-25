package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/trading-bot/bot-engine/internal/domain/ports"
	"github.com/trading-bot/bot-engine/internal/infrastructure/http/apierror"
	"github.com/trading-bot/bot-engine/internal/infrastructure/reqid"
)

// PortfolioHandler handles portfolio-related HTTP endpoints.
type PortfolioHandler struct {
	broker    ports.BrokerPort
	paperMode bool
	logger    *zap.Logger
}

// NewPortfolioHandler constructs a PortfolioHandler. paperMode must be the
// *verified* mode from config, never a hardcoded value.
// Routes are mounted by the server package's route-scope table.
func NewPortfolioHandler(broker ports.BrokerPort, paperMode bool, logger *zap.Logger) *PortfolioHandler {
	return &PortfolioHandler{broker: broker, paperMode: paperMode, logger: logger}
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
//	@Failure      502  {object}  apierror.Body  "broker_unavailable"
//	@Router       /api/v1/portfolio/equity [get]
func (h *PortfolioHandler) GetEquity(c *gin.Context) {
	equity, err := h.broker.GetPortfolioEquity(c.Request.Context())
	if err != nil {
		h.logger.Error("portfolio.equity_failed", zap.Error(err), zap.String("request_id", reqid.FromContext(c.Request.Context())))
		apierror.Abort(c, http.StatusBadGateway, apierror.CodeBrokerUnavailable, "the broker did not return the account equity")
		return
	}

	marketOpen, _ := h.broker.IsMarketOpen(c.Request.Context())

	c.JSON(http.StatusOK, equityResponse{
		Equity:     equity,
		PaperMode:  h.paperMode,
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
//	@Failure      502  {object}  apierror.Body  "broker_unavailable"
//	@Router       /api/v1/portfolio/positions [get]
func (h *PortfolioHandler) GetPositions(c *gin.Context) {
	positions, err := h.broker.GetPositions(c.Request.Context())
	if err != nil {
		h.logger.Error("portfolio.positions_failed", zap.Error(err), zap.String("request_id", reqid.FromContext(c.Request.Context())))
		apierror.Abort(c, http.StatusBadGateway, apierror.CodeBrokerUnavailable, "the broker did not return the open positions")
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

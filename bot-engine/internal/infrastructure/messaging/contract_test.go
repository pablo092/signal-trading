package messaging

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/trading-bot/bot-engine/internal/application/usecases"
	"github.com/trading-bot/bot-engine/internal/contracttest"
	"github.com/trading-bot/bot-engine/internal/domain/entities"
	"github.com/trading-bot/bot-engine/internal/domain/ports"
)

// BL-16 producer test: every event the hub sends conforms to ws-event.schema.json.
func TestEncodedEventsMatchTheContract(t *testing.T) {
	limit := decimal.RequireFromString("149.5")
	open, _ := entities.NewLimitOrder("MSFT", entities.OrderSideSell, decimal.RequireFromString("0.5"), limit, "ma_cross", "sig-2")

	events := map[string]ports.DomainEvent{
		"signal": {Type: usecases.EventSignalReceived, Payload: &ports.Signal{
			ID: "6f1c2d3e-4b5a-4c6d-8e7f-9a0b1c2d3e4f", Symbol: "AAPL", Direction: "HOLD", Strength: "WEAK",
			Price: decimal.RequireFromString("189.42"), Confidence: 0.4, Reason: "neutral", StrategyName: "RSIStrategy(period=14)",
			GeneratedAt: time.Now(),
		}},
		"filled order":  {Type: usecases.EventOrderPlaced, Payload: sampleOrder()},
		"working limit": {Type: usecases.EventOrderPlaced, Payload: open},
		"kill switch": {Type: usecases.EventKillSwitchChanged, Payload: usecases.KillSwitchStatus{
			KillSwitchState: ports.KillSwitchState{Engaged: true, Reason: "halt", UpdatedBy: "oncall", UpdatedAt: time.Now().UTC()},
		}},
	}
	for name, ev := range events {
		t.Run(name, func(t *testing.T) {
			msg, ok, err := EncodeEvent(ev, time.Now().UTC())
			if err != nil || !ok {
				t.Fatalf("encode: ok=%v err=%v", ok, err)
			}
			contracttest.Validate(t, "ws-event.schema.json", msg)
		})
	}
}

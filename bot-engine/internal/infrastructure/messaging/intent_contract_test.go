package messaging

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/trading-bot/bot-engine/internal/application/usecases"
	"github.com/trading-bot/bot-engine/internal/contracttest"
	"github.com/trading-bot/bot-engine/internal/domain/entities"
	"github.com/trading-bot/bot-engine/internal/domain/ports"
)

func sampleIntent(t *testing.T) *entities.OrderIntent {
	t.Helper()
	now := time.Now()
	it, err := entities.NewOrderIntent("AAPL", "rsi", entities.SignalSnapshot{
		ID: "6f1c2d3e-4b5a-4c6d-8e7f-9a0b1c2d3e4f", Direction: "BUY", Confidence: 0.8,
		Price: decimal.RequireFromString("150"), GeneratedAt: now,
	}, entities.Actor{ID: "mcp-gateway", Type: "agent"}, "key-00000001", "h", now, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	it.Side, it.Qty, it.EstimatedNotional = entities.OrderSideBuy, decimal.RequireFromString("6.666666"), decimal.RequireFromString("999.9999")
	it.Risk = entities.RiskReport{Passed: true, Stage: entities.RiskStageCreation, EvaluatedAt: now,
		Checks: []entities.RiskCheck{{Name: "kill_switch", Passed: true}}}
	return it
}

// BL-21: the intent lifecycle events conform to ws-event.schema.json.
func TestIntentEventsMatchTheContract(t *testing.T) {
	pending := sampleIntent(t)
	pending, _ = pending.Transition(entities.IntentPendingApproval, time.Now())
	approved, _ := pending.Decide(entities.IntentApproved, entities.Actor{ID: "owner", Type: "human"}, "", time.Now())
	filled, _ := approved.Transition(entities.IntentFilled, time.Now())
	rejected, _ := pending.Reject(entities.RejectionRiskFailed, "signal_fresh failed", time.Now())

	events := map[string]ports.DomainEvent{
		"pending intent":  {Type: usecases.EventIntentUpdated, Payload: usecases.IntentView{Intent: pending}},
		"filled intent":   {Type: usecases.EventIntentUpdated, Payload: usecases.IntentView{Intent: filled, Order: sampleOrder()}},
		"rejected intent": {Type: usecases.EventIntentUpdated, Payload: usecases.IntentView{Intent: rejected}},
		"order filled":    {Type: usecases.EventOrderFilled, Payload: sampleOrder()},
	}
	wantType := map[string]string{"pending intent": TypeIntentUpdated, "filled intent": TypeIntentUpdated, "rejected intent": TypeIntentUpdated, "order filled": TypeOrderFilled}
	for name, ev := range events {
		t.Run(name, func(t *testing.T) {
			msg, ok, err := EncodeEvent(ev, time.Now().UTC())
			if err != nil || !ok {
				t.Fatalf("encode: ok=%v err=%v", ok, err)
			}
			var env struct{ Type string }
			if err := json.Unmarshal(msg, &env); err != nil || env.Type != wantType[name] {
				t.Fatalf("type = %q (err %v), want %q", env.Type, err, wantType[name])
			}
			contracttest.Validate(t, "ws-event.schema.json", msg)
		})
	}
}

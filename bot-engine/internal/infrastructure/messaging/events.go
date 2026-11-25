package messaging

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/trading-bot/bot-engine/internal/application/usecases"
	"github.com/trading-bot/bot-engine/internal/domain/entities"
	"github.com/trading-bot/bot-engine/internal/domain/ports"
)

// EventVersion is the WebSocket envelope version (contracts/schemas/ws-event.schema.json).
const EventVersion = 1

// Contract event types.
const (
	TypeSignalReceived    = "signal.received"
	TypeOrderPlaced       = "order.placed"
	TypeKillSwitchChanged = "kill_switch.changed"
)

// Envelope is every WebSocket message the engine sends.
type Envelope struct {
	V    int       `json:"v"`
	Type string    `json:"type"`
	ID   string    `json:"id"`
	TS   time.Time `json:"ts"`
	Data any       `json:"data"`
}

// SignalData mirrors signal-service's SignalResponse (contracts/schemas/signal.schema.json).
type SignalData struct {
	ID           string      `json:"id"`
	Symbol       string      `json:"symbol"`
	Direction    string      `json:"direction"`
	Strength     string      `json:"strength"`
	Price        json.Number `json:"price"`
	Confidence   float64     `json:"confidence"`
	Reason       string      `json:"reason"`
	StrategyName string      `json:"strategy_name"`
	IsActionable bool        `json:"is_actionable"`
	GeneratedAt  time.Time   `json:"generated_at"`
}

// OrderData is an order as clients see it (contracts/schemas/order.schema.json).
// Amounts are JSON numbers printed from decimals, so no float rounding happens here.
type OrderData struct {
	ID          string       `json:"id"`
	Symbol      string       `json:"symbol"`
	Side        string       `json:"side"`
	Type        string       `json:"type"`
	Status      string       `json:"status"`
	Quantity    json.Number  `json:"quantity"`
	FilledQty   json.Number  `json:"filled_qty"`
	FilledPrice *json.Number `json:"filled_price"`
	LimitPrice  *json.Number `json:"limit_price"`
	BrokerID    string       `json:"broker_id"`
	StrategyID  string       `json:"strategy_id"`
	SignalID    string       `json:"signal_id"`
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
	FilledAt    *time.Time   `json:"filled_at"`
}

// KillSwitchData is the kill-switch state (contracts/schemas/kill-switch.schema.json).
type KillSwitchData struct {
	Engaged   bool      `json:"engaged"`
	Forced    bool      `json:"forced"`
	Reason    string    `json:"reason"`
	UpdatedBy string    `json:"updated_by"`
	UpdatedAt time.Time `json:"updated_at"`
}

// EncodeEvent converts a domain event into its contract JSON. ok is false for
// event types that are not relayed to clients.
func EncodeEvent(e ports.DomainEvent, now time.Time) (msg []byte, ok bool, err error) {
	var typ string
	var data any
	switch p := e.Payload.(type) {
	case *ports.Signal:
		typ, data = TypeSignalReceived, NewSignalData(p)
	case *entities.Order:
		if e.Type != usecases.EventOrderPlaced {
			return nil, false, nil
		}
		typ, data = TypeOrderPlaced, NewOrderData(p)
	case usecases.KillSwitchStatus:
		typ, data = TypeKillSwitchChanged, KillSwitchData{Engaged: p.Engaged, Forced: p.Forced, Reason: p.Reason, UpdatedBy: p.UpdatedBy, UpdatedAt: p.UpdatedAt}
	default:
		return nil, false, nil
	}
	b, err := json.Marshal(Envelope{V: EventVersion, Type: typ, ID: uuid.NewString(), TS: now, Data: data})
	if err != nil {
		return nil, false, fmt.Errorf("encode %s: %w", typ, err)
	}
	return b, true, nil
}

// NewSignalData converts a signal.
func NewSignalData(s *ports.Signal) SignalData {
	return SignalData{
		ID: s.ID, Symbol: s.Symbol, Direction: s.Direction, Strength: s.Strength, Price: num(s.Price),
		Confidence: s.Confidence, Reason: s.Reason, StrategyName: s.StrategyName,
		IsActionable: s.IsActionable, GeneratedAt: s.GeneratedAt.UTC(),
	}
}

// NewOrderData converts an order.
func NewOrderData(o *entities.Order) OrderData {
	return OrderData{
		ID: o.ID.String(), Symbol: o.Symbol, Side: string(o.Side), Type: string(o.Type), Status: string(o.Status),
		Quantity: num(o.Quantity), FilledQty: num(o.FilledQty), FilledPrice: numPtr(o.FilledPrice), LimitPrice: numPtr(o.LimitPrice),
		BrokerID: o.BrokerID, StrategyID: o.StrategyID, SignalID: o.SignalID,
		CreatedAt: o.CreatedAt.UTC(), UpdatedAt: o.UpdatedAt.UTC(), FilledAt: o.FilledAt,
	}
}

func num(d decimal.Decimal) json.Number { return json.Number(d.String()) }

func numPtr(d *decimal.Decimal) *json.Number {
	if d == nil {
		return nil
	}
	n := num(*d)
	return &n
}

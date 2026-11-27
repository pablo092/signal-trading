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
	TypeIntentUpdated     = "intent.updated"
	TypeOrderFilled       = "order.filled"
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
		switch e.Type {
		case usecases.EventOrderPlaced:
			typ, data = TypeOrderPlaced, NewOrderData(p)
		case usecases.EventOrderFilled:
			typ, data = TypeOrderFilled, NewOrderData(p)
		default:
			return nil, false, nil
		}
	case usecases.IntentView:
		typ, data = TypeIntentUpdated, NewIntentData(p)
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

// IntentData is an order intent as clients see it (contracts/schemas/order-intent.schema.json).
// It is also the HTTP body of the /api/v1/order-intents routes.
type IntentData struct {
	ID                string           `json:"id"`
	Status            string           `json:"status"`
	Symbol            string           `json:"symbol"`
	Strategy          string           `json:"strategy"`
	Side              *string          `json:"side"`
	Qty               json.Number      `json:"qty"`
	EstimatedNotional json.Number      `json:"estimated_notional"`
	Signal            IntentSignal     `json:"signal"`
	CreatedBy         ActorData        `json:"created_by"`
	CreatedAt         time.Time        `json:"created_at"`
	UpdatedAt         time.Time        `json:"updated_at"`
	ExpiresAt         time.Time        `json:"expires_at"`
	DecidedBy         *ActorData       `json:"decided_by"`
	DecidedAt         *time.Time       `json:"decided_at"`
	DecisionReason    *string          `json:"decision_reason"`
	RejectionCode     *string          `json:"rejection_code"`
	Risk              RiskData         `json:"risk"`
	Order             *IntentOrderData `json:"order"`
}

// IntentSignal is the signal an intent was created from.
type IntentSignal struct {
	ID          string      `json:"id"`
	Direction   string      `json:"direction"`
	Confidence  float64     `json:"confidence"`
	Price       json.Number `json:"price"`
	GeneratedAt time.Time   `json:"generated_at"`
	Reason      string      `json:"reason,omitempty"`
}

// ActorData identifies who acted.
type ActorData struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

// RiskData is the latest risk evaluation of an intent.
type RiskData struct {
	Passed      bool            `json:"passed"`
	Stage       string          `json:"stage"`
	EvaluatedAt time.Time       `json:"evaluated_at"`
	Checks      []RiskCheckData `json:"checks"`
}

// RiskCheckData is one evaluated rule.
type RiskCheckData struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Detail string `json:"detail"`
}

// IntentOrderData is the order an intent produced.
type IntentOrderData struct {
	ID          string       `json:"id"`
	BrokerID    string       `json:"broker_id"`
	Status      string       `json:"status"`
	FilledQty   json.Number  `json:"filled_qty"`
	FilledPrice *json.Number `json:"filled_price"`
	FilledAt    *time.Time   `json:"filled_at"`
}

// NewIntentData converts an intent and its order.
func NewIntentData(v usecases.IntentView) IntentData {
	it := v.Intent
	d := IntentData{
		ID: it.ID.String(), Status: string(it.Status), Symbol: it.Symbol, Strategy: it.Strategy,
		Qty: num(it.Qty), EstimatedNotional: num(it.EstimatedNotional),
		Signal: IntentSignal{ID: it.Signal.ID, Direction: it.Signal.Direction, Confidence: it.Signal.Confidence,
			Price: num(it.Signal.Price), GeneratedAt: it.Signal.GeneratedAt.UTC(), Reason: it.Signal.Reason},
		CreatedBy: ActorData{ID: it.CreatedBy.ID, Type: it.CreatedBy.Type},
		CreatedAt: it.CreatedAt.UTC(), UpdatedAt: it.UpdatedAt.UTC(), ExpiresAt: it.ExpiresAt.UTC(),
		DecidedAt: it.DecidedAt,
		Risk: RiskData{Passed: it.Risk.Passed, Stage: string(it.Risk.Stage), EvaluatedAt: it.Risk.EvaluatedAt.UTC(),
			Checks: make([]RiskCheckData, 0, len(it.Risk.Checks))},
	}
	if it.Risk.Stage == "" { // never evaluated (should not happen): report the creation stage, failed
		d.Risk.Stage, d.Risk.EvaluatedAt = string(entities.RiskStageCreation), it.CreatedAt.UTC()
	}
	if it.Side != "" {
		s := string(it.Side)
		d.Side = &s
	}
	if it.DecidedBy != nil {
		d.DecidedBy = &ActorData{ID: it.DecidedBy.ID, Type: it.DecidedBy.Type}
	}
	if it.DecisionReason != "" {
		r := it.DecisionReason
		d.DecisionReason = &r
	}
	if it.RejectionCode != "" {
		c := string(it.RejectionCode)
		d.RejectionCode = &c
	}
	for _, c := range it.Risk.Checks {
		d.Risk.Checks = append(d.Risk.Checks, RiskCheckData{Name: c.Name, Passed: c.Passed, Detail: c.Detail})
	}
	if o := v.Order; o != nil {
		d.Order = &IntentOrderData{ID: o.ID.String(), BrokerID: o.BrokerID, Status: string(o.Status),
			FilledQty: num(o.FilledQty), FilledPrice: numPtr(o.FilledPrice), FilledAt: o.FilledAt}
	}
	return d
}

func num(d decimal.Decimal) json.Number { return json.Number(d.String()) }

func numPtr(d *decimal.Decimal) *json.Number {
	if d == nil {
		return nil
	}
	n := num(*d)
	return &n
}

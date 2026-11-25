package entities

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// Actor is who caused an action: an authenticated principal or the engine itself.
type Actor struct {
	ID   string // principal ID, or "system"
	Type string // "human", "service", "agent" or "system"
}

// IntentStatus is the lifecycle state of an order intent (ADR-005 §5.3).
type IntentStatus string

const (
	IntentPendingRisk     IntentStatus = "pending_risk"
	IntentPendingApproval IntentStatus = "pending_approval"
	IntentApproved        IntentStatus = "approved"
	IntentSubmitted       IntentStatus = "submitted"
	IntentFilled          IntentStatus = "filled"
	IntentRejected        IntentStatus = "rejected"
	IntentCanceled        IntentStatus = "canceled"
	IntentExpired         IntentStatus = "expired"
)

// IntentStatuses lists every status, for input validation.
var IntentStatuses = []IntentStatus{
	IntentPendingRisk, IntentPendingApproval, IntentApproved, IntentSubmitted,
	IntentFilled, IntentRejected, IntentCanceled, IntentExpired,
}

// Active reports whether the intent can still lead to a new broker order. At
// most one active intent may exist per signal (ADR-005 §5.4).
func (s IntentStatus) Active() bool {
	return s == IntentPendingRisk || s == IntentPendingApproval || s == IntentApproved
}

// Terminal reports whether no further transition is possible.
func (s IntentStatus) Terminal() bool {
	return s == IntentFilled || s == IntentRejected || s == IntentCanceled || s == IntentExpired
}

// intentTransitions is the state machine. Anything not listed is refused.
var intentTransitions = map[IntentStatus][]IntentStatus{
	IntentPendingRisk:     {IntentPendingApproval, IntentRejected},
	IntentPendingApproval: {IntentApproved, IntentRejected, IntentCanceled, IntentExpired},
	// approved → rejected: risk re-check failed, audit unavailable, or the broker refused.
	IntentApproved: {IntentSubmitted, IntentFilled, IntentRejected, IntentCanceled, IntentExpired},
	// The broker's view after submission (reconciler, BL-23).
	IntentSubmitted: {IntentFilled, IntentRejected, IntentCanceled, IntentExpired},
}

// CanTransition reports whether from → to is allowed.
func CanTransition(from, to IntentStatus) bool {
	for _, s := range intentTransitions[from] {
		if s == to {
			return true
		}
	}
	return false
}

// RejectionCode says why an intent ended rejected.
type RejectionCode string

const (
	RejectionRiskFailed     RejectionCode = "risk_failed"
	RejectionByApprover     RejectionCode = "rejected_by_approver"
	RejectionBrokerRejected RejectionCode = "broker_rejected"
	RejectionKillSwitch     RejectionCode = "kill_switch"
	RejectionNone           RejectionCode = ""
)

// RiskStage is when a risk evaluation ran.
type RiskStage string

const (
	RiskStageCreation   RiskStage = "creation"
	RiskStageSubmission RiskStage = "submission"
)

// RiskCheck is one evaluated rule.
type RiskCheck struct {
	Name   string
	Passed bool
	Detail string
}

// RiskReport is a full evaluation. Passed is true only if every check passed.
type RiskReport struct {
	Passed      bool
	Stage       RiskStage
	EvaluatedAt time.Time
	Checks      []RiskCheck
}

// Failed returns the names of the failed checks.
func (r RiskReport) Failed() []string {
	var out []string
	for _, c := range r.Checks {
		if !c.Passed {
			out = append(out, c.Name)
		}
	}
	return out
}

// SignalSnapshot is the signal an intent was created from. It is stored, so
// the decision can be reviewed later even if signal-service changes.
type SignalSnapshot struct {
	ID          string
	Direction   string // BUY, SELL or HOLD
	Confidence  float64
	Price       decimal.Decimal
	GeneratedAt time.Time
}

// OrderIntent is a proposed order. Only an approved intent is ever submitted,
// and its ID doubles as the order ID and the broker client order ID.
type OrderIntent struct {
	ID                uuid.UUID
	Status            IntentStatus
	Symbol            string
	Strategy          string
	Side              OrderSide // "" when the signal is not actionable
	Qty               decimal.Decimal
	EstimatedNotional decimal.Decimal
	Signal            SignalSnapshot

	CreatedBy      Actor
	IdempotencyKey string
	RequestHash    string // sha256 of the creation request, to detect key reuse

	CreatedAt time.Time
	UpdatedAt time.Time
	ExpiresAt time.Time

	DecidedBy      *Actor
	DecidedAt      *time.Time
	DecisionReason string
	RejectionCode  RejectionCode

	Risk RiskReport
}

// NewOrderIntent creates an intent in pending_risk.
func NewOrderIntent(symbol, strategy string, signal SignalSnapshot, by Actor, key, requestHash string, now time.Time, ttl time.Duration) (*OrderIntent, error) {
	if symbol == "" || strategy == "" {
		return nil, fmt.Errorf("symbol and strategy are required")
	}
	if by.ID == "" {
		return nil, fmt.Errorf("intent needs an actor")
	}
	if ttl <= 0 {
		return nil, fmt.Errorf("intent TTL must be positive")
	}
	now = now.UTC()
	return &OrderIntent{
		ID: uuid.New(), Status: IntentPendingRisk, Symbol: symbol, Strategy: strategy, Signal: signal,
		CreatedBy: by, IdempotencyKey: key, RequestHash: requestHash,
		CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(ttl),
	}, nil
}

// Transition returns a copy in status to, or an error if the state machine
// does not allow it. Intents are immutable: callers persist the copy.
func (i *OrderIntent) Transition(to IntentStatus, now time.Time) (*OrderIntent, error) {
	if !CanTransition(i.Status, to) {
		return nil, fmt.Errorf("intent %s: %s -> %s is not allowed", i.ID, i.Status, to)
	}
	next := *i
	next.Status = to
	next.UpdatedAt = now.UTC()
	return &next, nil
}

// Decide returns a copy carrying who decided and why (approval, rejection or cancel).
func (i *OrderIntent) Decide(to IntentStatus, by Actor, reason string, now time.Time) (*OrderIntent, error) {
	next, err := i.Transition(to, now)
	if err != nil {
		return nil, err
	}
	t := now.UTC()
	next.DecidedBy, next.DecidedAt, next.DecisionReason = &by, &t, reason
	return next, nil
}

// Reject returns a copy in rejected with a code and reason.
func (i *OrderIntent) Reject(code RejectionCode, reason string, now time.Time) (*OrderIntent, error) {
	next, err := i.Transition(IntentRejected, now)
	if err != nil {
		return nil, err
	}
	next.RejectionCode = code
	if next.DecisionReason == "" {
		next.DecisionReason = reason
	}
	return next, nil
}

// Expired reports whether an intent waiting for approval is past its deadline.
func (i *OrderIntent) Expired(now time.Time) bool {
	return i.Status == IntentPendingApproval && !now.Before(i.ExpiresAt)
}

// IntentStatusForOrder maps a broker order status onto the intent lifecycle.
func IntentStatusForOrder(s OrderStatus) IntentStatus {
	switch s {
	case OrderStatusFilled:
		return IntentFilled
	case OrderStatusCanceled:
		return IntentCanceled
	case OrderStatusExpired:
		return IntentExpired
	case OrderStatusRejected:
		return IntentRejected
	default: // pending (accepted, not yet working), submitted, partially_filled
		return IntentSubmitted
	}
}

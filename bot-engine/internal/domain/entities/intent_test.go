package entities

import (
	"testing"
	"time"
)

func TestIntentStateMachine(t *testing.T) {
	allowed := map[IntentStatus][]IntentStatus{
		IntentPendingRisk:     {IntentPendingApproval, IntentRejected},
		IntentPendingApproval: {IntentApproved, IntentRejected, IntentCanceled, IntentExpired},
		IntentApproved:        {IntentSubmitted, IntentFilled, IntentRejected, IntentCanceled, IntentExpired},
		IntentSubmitted:       {IntentFilled, IntentRejected, IntentCanceled, IntentExpired},
	}
	for _, from := range IntentStatuses {
		ok := map[IntentStatus]bool{}
		for _, to := range allowed[from] {
			ok[to] = true
		}
		for _, to := range IntentStatuses {
			if got := CanTransition(from, to); got != ok[to] {
				t.Errorf("CanTransition(%s, %s) = %v, want %v", from, to, got, ok[to])
			}
		}
		if from.Terminal() == (len(allowed[from]) > 0) {
			t.Errorf("%s: Terminal() = %v but it has %d exits", from, from.Terminal(), len(allowed[from]))
		}
	}
}

func newTestIntent(t *testing.T, now time.Time) *OrderIntent {
	t.Helper()
	it, err := NewOrderIntent("AAPL", "rsi", SignalSnapshot{ID: "sig-1"}, Actor{ID: "gw", Type: "agent"}, "key-00000001", "h", now, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return it
}

func TestIntentTransitionsAreCopies(t *testing.T) {
	now := time.Now()
	it := newTestIntent(t, now)
	next, err := it.Transition(IntentPendingApproval, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if it.Status != IntentPendingRisk || next.Status != IntentPendingApproval {
		t.Fatalf("original mutated or copy wrong: %s / %s", it.Status, next.Status)
	}
	if _, err := next.Transition(IntentFilled, now); err == nil {
		t.Fatal("pending_approval -> filled must be refused")
	}
	by := Actor{ID: "owner", Type: "human"}
	ok, err := next.Decide(IntentApproved, by, "looks fine", now)
	if err != nil || ok.DecidedBy == nil || *ok.DecidedBy != by || ok.DecisionReason != "looks fine" {
		t.Fatalf("Decide: %+v err=%v", ok, err)
	}
	rej, err := next.Reject(RejectionByApprover, "no", now)
	if err != nil || rej.RejectionCode != RejectionByApprover || rej.DecisionReason != "no" {
		t.Fatalf("Reject: %+v err=%v", rej, err)
	}
}

func TestIntentExpiry(t *testing.T) {
	now := time.Now()
	it := newTestIntent(t, now)
	if it.Expired(now.Add(time.Hour)) {
		t.Fatal("only intents waiting for approval expire")
	}
	pending, _ := it.Transition(IntentPendingApproval, now)
	if pending.Expired(now.Add(4 * time.Minute)) {
		t.Fatal("expired before its TTL")
	}
	if !pending.Expired(now.Add(5 * time.Minute)) {
		t.Fatal("not expired at its deadline")
	}
}

func TestNewOrderIntentValidation(t *testing.T) {
	now := time.Now()
	by := Actor{ID: "gw", Type: "agent"}
	if _, err := NewOrderIntent("", "rsi", SignalSnapshot{}, by, "k", "h", now, time.Minute); err == nil {
		t.Error("empty symbol accepted")
	}
	if _, err := NewOrderIntent("AAPL", "rsi", SignalSnapshot{}, Actor{}, "k", "h", now, time.Minute); err == nil {
		t.Error("missing actor accepted")
	}
	if _, err := NewOrderIntent("AAPL", "rsi", SignalSnapshot{}, by, "k", "h", now, 0); err == nil {
		t.Error("zero TTL accepted")
	}
}

func TestIntentStatusForOrder(t *testing.T) {
	cases := map[OrderStatus]IntentStatus{
		OrderStatusPending: IntentSubmitted, OrderStatusSubmitted: IntentSubmitted, OrderStatusPartiallyFilled: IntentSubmitted,
		OrderStatusFilled: IntentFilled, OrderStatusCanceled: IntentCanceled, OrderStatusExpired: IntentExpired,
		OrderStatusRejected: IntentRejected,
	}
	for o, want := range cases {
		if got := IntentStatusForOrder(o); got != want {
			t.Errorf("IntentStatusForOrder(%s) = %s, want %s", o, got, want)
		}
	}
}

package metrics

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/shopspring/decimal"

	"github.com/trading-bot/bot-engine/internal/application/usecases"
	"github.com/trading-bot/bot-engine/internal/domain/entities"
	"github.com/trading-bot/bot-engine/internal/domain/ports"
)

// value reads one series from reg (counter or gauge); labels are name=value pairs.
func value(t *testing.T, reg *prometheus.Registry, name string, labels ...string) float64 {
	t.Helper()
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
	series:
		for _, m := range mf.GetMetric() {
			got := map[string]string{}
			for _, l := range m.GetLabel() {
				got[l.GetName()] = l.GetValue()
			}
			for i := 0; i+1 < len(labels); i += 2 {
				if got[labels[i]] != labels[i+1] {
					continue series
				}
			}
			if m.GetCounter() != nil {
				return m.GetCounter().GetValue()
			}
			return m.GetGauge().GetValue()
		}
	}
	return -1
}

func series(t *testing.T, reg *prometheus.Registry, name string) int {
	t.Helper()
	mfs, _ := reg.Gather()
	for _, mf := range mfs {
		if mf.GetName() == name {
			return len(mf.GetMetric())
		}
	}
	return 0
}

func intentIn(t *testing.T, status entities.IntentStatus, code entities.RejectionCode, failed ...string) usecases.IntentView {
	t.Helper()
	it, err := entities.NewOrderIntent("AAPL", "rsi", entities.SignalSnapshot{ID: "s"}, entities.Actor{ID: "gw", Type: "agent"}, "key-00000001", "h", time.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	it.Status, it.RejectionCode = status, code
	it.Risk.Stage = entities.RiskStageSubmission
	it.Risk.Checks = []entities.RiskCheck{{Name: "kill_switch", Passed: true}}
	for _, f := range failed {
		it.Risk.Checks = append(it.Risk.Checks, entities.RiskCheck{Name: f})
	}
	return usecases.IntentView{Intent: it}
}

func TestTradingMetricsFollowDomainEvents(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewTrading(reg)
	ctx := context.Background()

	m.HandleDomainEvent(ctx, ports.DomainEvent{Type: usecases.EventIntentUpdated, Payload: intentIn(t, entities.IntentPendingApproval, "")})
	m.HandleDomainEvent(ctx, ports.DomainEvent{Type: usecases.EventIntentUpdated, Payload: intentIn(t, entities.IntentRejected, entities.RejectionRiskFailed, "signal_fresh", "buying_power")})
	m.HandleDomainEvent(ctx, ports.DomainEvent{Type: usecases.EventIntentUpdated, Payload: intentIn(t, entities.IntentRejected, entities.RejectionByApprover)})
	order, _ := entities.NewMarketOrder("AAPL", entities.OrderSideBuy, decimal.NewFromInt(1), "rsi", "s")
	m.HandleDomainEvent(ctx, ports.DomainEvent{Type: usecases.EventOrderFilled, Payload: order})
	m.HandleDomainEvent(ctx, ports.DomainEvent{Type: usecases.EventIntentUpdated, Payload: "not a view"}) // ignored

	if got := value(t, reg, "order_intent_transitions_total", "status", "pending_approval"); got != 1 {
		t.Errorf("pending_approval = %v", got)
	}
	if got := value(t, reg, "order_intent_transitions_total", "status", "rejected", "rejection_code", "risk_failed"); got != 1 {
		t.Errorf("rejected/risk_failed = %v", got)
	}
	if got := value(t, reg, "risk_check_failures_total", "check", "signal_fresh", "stage", "submission"); got != 1 {
		t.Errorf("signal_fresh failures = %v", got)
	}
	if got := series(t, reg, "risk_check_failures_total"); got != 2 {
		t.Errorf("only the failed checks of risk rejections count, got %d series", got)
	}
	if got := value(t, reg, "orders_filled_total", "side", "buy"); got != 1 {
		t.Errorf("fills = %v", got)
	}
}

func TestReconcilerObserver(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewTrading(reg)
	m.StreamUp(true)
	if value(t, reg, "reconciler_trade_stream_up") != 1 {
		t.Fatal("stream should be up")
	}
	m.StreamUp(false)
	if value(t, reg, "reconciler_trade_stream_up") != 0 || value(t, reg, "reconciler_trade_stream_disconnects_total") != 1 {
		t.Fatal("disconnect not recorded")
	}
	m.Reconciled(usecases.ReconciledByPoll)
	m.Polled(time.Unix(1_700_000_000, 0))
	if value(t, reg, "reconciler_orders_reconciled_total", "source", "poll") != 1 || value(t, reg, "reconciler_last_poll_timestamp_seconds") != 1_700_000_000 {
		t.Fatal("poll not recorded")
	}
}

func TestScannerObserver(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewTrading(reg)
	if value(t, reg, "scanner_opportunities_total") != 0 {
		t.Fatal("opportunities should start at 0")
	}
	m.OpportunityFound()
	m.Scanned(time.Unix(1_700_000_000, 0))
	if value(t, reg, "scanner_opportunities_total") != 1 || value(t, reg, "scanner_last_scan_timestamp_seconds") != 1_700_000_000 {
		t.Fatal("scan not recorded")
	}
}

func TestSeriesExistAtZero(t *testing.T) {
	reg := prometheus.NewRegistry()
	NewTrading(reg)
	if series(t, reg, "reconciler_orders_reconciled_total") != 3 {
		t.Fatal("reconciled sources should be pre-created")
	}
}

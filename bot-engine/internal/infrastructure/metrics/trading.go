// Package metrics exports trading-lifecycle metrics (M4 paper operations):
// intent transitions, risk-check failures, fills and reconciler health. It
// reads domain events from the bus and implements usecases.ReconcilerObserver.
// Labels are bounded enums only: never a symbol, actor or ID.
package metrics

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/trading-bot/bot-engine/internal/application/usecases"
	"github.com/trading-bot/bot-engine/internal/domain/entities"
	"github.com/trading-bot/bot-engine/internal/domain/ports"
)

// Trading holds the trading metrics.
type Trading struct {
	intentTransitions *prometheus.CounterVec
	riskFailures      *prometheus.CounterVec
	ordersFilled      *prometheus.CounterVec
	reconciled        *prometheus.CounterVec
	streamUp          prometheus.Gauge
	streamReconnects  prometheus.Counter
	lastPoll          prometheus.Gauge
	opportunities     prometheus.Counter
	lastScan          prometheus.Gauge
}

// NewTrading registers the trading metrics on reg.
func NewTrading(reg prometheus.Registerer) *Trading {
	m := &Trading{
		intentTransitions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "order_intent_transitions_total",
			Help: "Order intents entering a status (rejection_code is empty unless status=rejected).",
		}, []string{"status", "rejection_code"}),
		riskFailures: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "risk_check_failures_total",
			Help: "Failed risk checks on intents rejected by the risk engine, by check and stage.",
		}, []string{"check", "stage"}),
		ordersFilled: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "orders_filled_total",
			Help: "Orders the broker reported filled.",
		}, []string{"side"}),
		reconciled: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "reconciler_orders_reconciled_total",
			Help: "Local orders changed to match the broker, by source (stream, poll, not_placed).",
		}, []string{"source"}),
		streamUp: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "reconciler_trade_stream_up",
			Help: "1 while the broker trade-update stream call is running.",
		}),
		streamReconnects: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "reconciler_trade_stream_disconnects_total",
			Help: "Times the trade-update stream ended and had to reconnect.",
		}),
		lastPoll: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "reconciler_last_poll_timestamp_seconds",
			Help: "Unix time of the last completed reconciliation poll.",
		}),
		opportunities: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "scanner_opportunities_total",
			Help: "Order intents the market scanner proposed that passed risk and wait for approval.",
		}),
		lastScan: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "scanner_last_scan_timestamp_seconds",
			Help: "Unix time of the last market scan (including scans skipped because the market is closed).",
		}),
	}
	reg.MustRegister(m.intentTransitions, m.riskFailures, m.ordersFilled, m.reconciled, m.streamUp, m.streamReconnects, m.lastPoll, m.opportunities, m.lastScan)
	// Pre-create the series dashboards and alerts read, so they exist at 0.
	for _, st := range entities.IntentStatuses {
		if st != entities.IntentRejected {
			m.intentTransitions.WithLabelValues(string(st), "")
		}
	}
	for _, src := range []string{usecases.ReconciledByStream, usecases.ReconciledByPoll, usecases.ReconciledNeverPlaced} {
		m.reconciled.WithLabelValues(src)
	}
	return m
}

// HandleDomainEvent is an event-bus subscriber for EventIntentUpdated and EventOrderFilled.
func (m *Trading) HandleDomainEvent(_ context.Context, ev ports.DomainEvent) {
	switch ev.Type {
	case usecases.EventIntentUpdated:
		v, ok := ev.Payload.(usecases.IntentView)
		if !ok || v.Intent == nil {
			return
		}
		it := v.Intent
		m.intentTransitions.WithLabelValues(string(it.Status), string(it.RejectionCode)).Inc()
		if it.Status == entities.IntentRejected && it.RejectionCode == entities.RejectionRiskFailed {
			for _, c := range it.Risk.Checks {
				if !c.Passed {
					m.riskFailures.WithLabelValues(c.Name, string(it.Risk.Stage)).Inc()
				}
			}
		}
	case usecases.EventOrderFilled:
		if o, ok := ev.Payload.(*entities.Order); ok && o != nil {
			m.ordersFilled.WithLabelValues(string(o.Side)).Inc()
		}
	}
}

// StreamUp implements usecases.ReconcilerObserver.
func (m *Trading) StreamUp(up bool) {
	if up {
		m.streamUp.Set(1)
		return
	}
	m.streamUp.Set(0)
	m.streamReconnects.Inc()
}

// Reconciled implements usecases.ReconcilerObserver.
func (m *Trading) Reconciled(source string) { m.reconciled.WithLabelValues(source).Inc() }

// Polled implements usecases.ReconcilerObserver.
func (m *Trading) Polled(at time.Time) { m.lastPoll.Set(float64(at.Unix())) }

// Scanned implements usecases.ScannerObserver.
func (m *Trading) Scanned(at time.Time) { m.lastScan.Set(float64(at.Unix())) }

// OpportunityFound implements usecases.ScannerObserver.
func (m *Trading) OpportunityFound() { m.opportunities.Inc() }

var (
	_ usecases.ReconcilerObserver = (*Trading)(nil)
	_ usecases.ScannerObserver    = (*Trading)(nil)
)

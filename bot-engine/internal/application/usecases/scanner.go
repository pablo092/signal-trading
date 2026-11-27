package usecases

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/trading-bot/bot-engine/internal/domain/entities"
	"github.com/trading-bot/bot-engine/internal/domain/ports"
)

// ScannerActor is the identity the market scanner proposes intents with. It
// can only propose: approval still needs a human (ADR-005 §5.3).
var ScannerActor = ports.Actor{ID: "scanner", Type: "system"}

// ScannerPolicy configures the market scanner.
type ScannerPolicy struct {
	Interval   time.Duration
	Symbols    []string // usually the risk symbol allowlist
	Strategies []string // usually the risk strategy allowlist
	// MinConfidence skips signals below it before proposing (the risk engine
	// enforces its own minimum on top).
	MinConfidence float64
	// IgnoreMarketHours scans outside regular hours (RISK_ALLOW_EXTENDED_HOURS).
	IgnoreMarketHours bool
}

// Validate rejects a policy the scanner cannot run with.
func (p ScannerPolicy) Validate() error {
	switch {
	case p.Interval < time.Minute:
		return fmt.Errorf("scanner interval must be at least 1m, got %s", p.Interval)
	case len(p.Symbols) == 0 || len(p.Strategies) == 0:
		return fmt.Errorf("scanner needs at least one symbol and one strategy")
	}
	return nil
}

// ScannerObserver receives scanner metrics.
type ScannerObserver interface {
	Scanned(at time.Time)
	OpportunityFound()
}

// IntentProposer is the part of IntentService the scanner uses.
type IntentProposer interface {
	Create(ctx context.Context, in CreateIntentInput) (CreateResult, error)
	List(ctx context.Context, f ports.IntentFilter) ([]IntentView, error)
}

// MarketClockReader reads the broker's market clock.
type MarketClockReader interface {
	GetClock(ctx context.Context) (ports.MarketClock, error)
}

// ScannerDeps are the Scanner dependencies. Observer is optional.
type ScannerDeps struct {
	Intents    IntentProposer
	Signals    ports.SignalServicePort
	Clock      MarketClockReader
	KillSwitch KillSwitchChecker
	Observer   ScannerObserver
	Policy     ScannerPolicy
	Logger     *zap.Logger
}

// Scanner watches the market for the owner: every interval, during market
// hours, it asks each strategy about each symbol and proposes an order intent
// for an actionable signal. The intent goes through the same risk checks as
// any other and waits in pending_approval for a human decision. The scanner
// never approves, and it proposes at most one pending intent per symbol.
type Scanner struct {
	d   ScannerDeps
	now func() time.Time
}

// NewScanner builds a scanner.
func NewScanner(d ScannerDeps) *Scanner {
	return &Scanner{d: d, now: time.Now}
}

// ScanResult summarizes one scan.
type ScanResult struct {
	Skipped       string // non-empty when the scan did not look at signals
	Proposed      int    // intents that reached pending_approval
	RiskRejected  int    // actionable signals the risk engine refused
	SignalErrors  int
	Considered    int // symbol and strategy pairs asked
	AlreadyActive int // symbols skipped because an intent is already pending
}

// Scan runs one pass.
func (s *Scanner) Scan(ctx context.Context) ScanResult {
	log := s.d.Logger.With(zap.String("component", "scanner"))
	res := s.scan(ctx, log)
	if s.d.Observer != nil {
		s.d.Observer.Scanned(s.now())
	}
	if res.Skipped != "" {
		log.Debug("scanner.skipped", zap.String("reason", res.Skipped))
	} else {
		log.Info("scanner.scanned", zap.Int("proposed", res.Proposed), zap.Int("risk_rejected", res.RiskRejected),
			zap.Int("signal_errors", res.SignalErrors), zap.Int("already_pending", res.AlreadyActive))
	}
	return res
}

func (s *Scanner) scan(ctx context.Context, log *zap.Logger) ScanResult {
	if engaged, err := s.d.KillSwitch.Engaged(ctx); err != nil || engaged {
		return ScanResult{Skipped: "kill_switch"}
	}
	if !s.d.Policy.IgnoreMarketHours {
		clock, err := s.d.Clock.GetClock(ctx)
		if err != nil {
			log.Warn("scanner.clock_unavailable", zap.Error(err))
			return ScanResult{Skipped: "clock_unavailable"}
		}
		if !clock.IsOpen {
			return ScanResult{Skipped: "market_closed"}
		}
	}

	pending, err := s.d.Intents.List(ctx, ports.IntentFilter{Status: entities.IntentPendingApproval, Limit: 200})
	if err != nil {
		log.Warn("scanner.list_failed", zap.Error(err))
		return ScanResult{Skipped: "intents_unavailable"}
	}
	busy := map[string]bool{}
	for _, v := range pending {
		if v.Intent != nil {
			busy[strings.ToUpper(v.Intent.Symbol)] = true
		}
	}

	var res ScanResult
	for _, symbol := range s.d.Policy.Symbols {
		symbol = strings.ToUpper(symbol)
		if busy[symbol] {
			res.AlreadyActive++
			continue
		}
		for _, strategy := range s.d.Policy.Strategies {
			res.Considered++
			sig, err := s.d.Signals.GetSignal(ctx, symbol, strategy)
			if err != nil {
				res.SignalErrors++
				log.Warn("scanner.signal_failed", zap.String("symbol", symbol), zap.String("strategy", strategy), zap.Error(err))
				continue
			}
			if !sig.IsActionable || sig.Confidence < s.d.Policy.MinConfidence {
				continue
			}
			out, err := s.d.Intents.Create(ctx, CreateIntentInput{
				Actor: ScannerActor, Symbol: symbol, Strategy: strategy,
				// One proposal per signal; Create fetches a fresh signal itself.
				IdempotencyKey: "scan-" + sig.ID,
			})
			switch {
			case errors.Is(err, ErrIntentRateLimited), errors.Is(err, ports.ErrKillSwitchEngaged):
				log.Warn("scanner.stopped", zap.Error(err))
				return res
			case err != nil:
				res.SignalErrors++
				log.Warn("scanner.propose_failed", zap.String("symbol", symbol), zap.String("strategy", strategy), zap.Error(err))
				continue
			}
			it := out.View.Intent
			if it == nil || out.Replayed {
				continue
			}
			if it.Status == entities.IntentPendingApproval {
				res.Proposed++
				if s.d.Observer != nil {
					s.d.Observer.OpportunityFound()
				}
				log.Info("scanner.opportunity", zap.String("intent_id", it.ID.String()), zap.String("symbol", symbol),
					zap.String("strategy", strategy), zap.String("side", string(it.Side)))
				break // one pending intent per symbol
			}
			res.RiskRejected++
		}
	}
	return res
}

// Run scans every interval until ctx ends.
func (s *Scanner) Run(ctx context.Context) {
	t := time.NewTicker(s.d.Policy.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.Scan(ctx)
		}
	}
}

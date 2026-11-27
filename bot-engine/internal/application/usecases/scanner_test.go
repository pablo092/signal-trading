package usecases_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/trading-bot/bot-engine/internal/application/usecases"
	"github.com/trading-bot/bot-engine/internal/domain/entities"
	"github.com/trading-bot/bot-engine/internal/domain/ports"
)

type scanSignals struct {
	byKey map[string]*ports.Signal // "SYMBOL/strategy"
	err   error
}

func (f scanSignals) GetSignal(_ context.Context, symbol, strategy string) (*ports.Signal, error) {
	if f.err != nil {
		return nil, f.err
	}
	if s, ok := f.byKey[symbol+"/"+strategy]; ok {
		return s, nil
	}
	return &ports.Signal{ID: "hold-" + symbol + strategy, Direction: "HOLD", Confidence: 0.1}, nil
}

type scanClock struct {
	open bool
	err  error
}

func (c scanClock) GetClock(context.Context) (ports.MarketClock, error) {
	return ports.MarketClock{IsOpen: c.open}, c.err
}

type scanProposer struct {
	mu      sync.Mutex
	pending []usecases.IntentView
	status  map[string]entities.IntentStatus // by symbol; default pending_approval
	err     error
	created []usecases.CreateIntentInput
}

func (p *scanProposer) Create(_ context.Context, in usecases.CreateIntentInput) (usecases.CreateResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.created = append(p.created, in)
	if p.err != nil {
		return usecases.CreateResult{}, p.err
	}
	st := entities.IntentPendingApproval
	if s, ok := p.status[in.Symbol]; ok {
		st = s
	}
	return usecases.CreateResult{View: usecases.IntentView{Intent: &entities.OrderIntent{Symbol: in.Symbol, Strategy: in.Strategy, Status: st, Side: entities.OrderSideBuy}}}, nil
}

func (p *scanProposer) List(context.Context, ports.IntentFilter) ([]usecases.IntentView, error) {
	return p.pending, nil
}

type scanObserver struct {
	scans, found int
}

func (o *scanObserver) Scanned(time.Time) { o.scans++ }
func (o *scanObserver) OpportunityFound() { o.found++ }

func buy(symbol string, conf float64) *ports.Signal {
	return &ports.Signal{ID: "sig-" + symbol, Symbol: symbol, Direction: "BUY", Confidence: conf, IsActionable: true}
}

func newTestScanner(sig scanSignals, clock scanClock, ks switchState, p *scanProposer, obs *scanObserver) *usecases.Scanner {
	return usecases.NewScanner(usecases.ScannerDeps{
		Intents: p, Signals: sig, Clock: clock, KillSwitch: ks, Observer: obs, Logger: zap.NewNop(),
		Policy: usecases.ScannerPolicy{
			Interval: time.Minute, Symbols: []string{"AAPL", "MSFT"}, Strategies: []string{"rsi", "ma_cross"},
			MinConfidence: 0.6,
		},
	})
}

func TestScannerProposesActionableSignals(t *testing.T) {
	p := &scanProposer{}
	obs := &scanObserver{}
	sig := scanSignals{byKey: map[string]*ports.Signal{"AAPL/rsi": buy("AAPL", 0.8)}}
	res := newTestScanner(sig, scanClock{open: true}, switchState{}, p, obs).Scan(context.Background())

	if res.Proposed != 1 || len(p.created) != 1 {
		t.Fatalf("want one proposal, got %+v (created %d)", res, len(p.created))
	}
	in := p.created[0]
	if in.Actor != usecases.ScannerActor || in.Symbol != "AAPL" || in.Strategy != "rsi" || in.IdempotencyKey != "scan-sig-AAPL" {
		t.Fatalf("unexpected proposal %+v", in)
	}
	if obs.found != 1 || obs.scans != 1 {
		t.Fatalf("observer: %+v", obs)
	}
}

func TestScannerSkipsHoldAndLowConfidence(t *testing.T) {
	p := &scanProposer{}
	sig := scanSignals{byKey: map[string]*ports.Signal{"MSFT/rsi": buy("MSFT", 0.5)}}
	res := newTestScanner(sig, scanClock{open: true}, switchState{}, p, &scanObserver{}).Scan(context.Background())
	if len(p.created) != 0 || res.Considered != 4 {
		t.Fatalf("nothing should be proposed: %+v, created %d", res, len(p.created))
	}
}

func TestScannerOnePendingIntentPerSymbol(t *testing.T) {
	sig := scanSignals{byKey: map[string]*ports.Signal{
		"AAPL/rsi": buy("AAPL", 0.8), "AAPL/ma_cross": buy("AAPL", 0.9), "MSFT/rsi": buy("MSFT", 0.9),
	}}
	// MSFT already has a pending intent; AAPL gets one proposal, not two.
	p := &scanProposer{pending: []usecases.IntentView{{Intent: &entities.OrderIntent{Symbol: "MSFT", Status: entities.IntentPendingApproval}}}}
	res := newTestScanner(sig, scanClock{open: true}, switchState{}, p, &scanObserver{}).Scan(context.Background())
	if res.Proposed != 1 || res.AlreadyActive != 1 || len(p.created) != 1 || p.created[0].Symbol != "AAPL" {
		t.Fatalf("got %+v, created %+v", res, p.created)
	}
}

func TestScannerTriesNextStrategyAfterRiskRejection(t *testing.T) {
	sig := scanSignals{byKey: map[string]*ports.Signal{"AAPL/rsi": buy("AAPL", 0.8), "AAPL/ma_cross": buy("AAPL", 0.9)}}
	p := &scanProposer{status: map[string]entities.IntentStatus{"AAPL": entities.IntentRejected}}
	res := newTestScanner(sig, scanClock{open: true}, switchState{}, p, &scanObserver{}).Scan(context.Background())
	if res.RiskRejected != 2 || res.Proposed != 0 {
		t.Fatalf("got %+v", res)
	}
}

func TestScannerSkips(t *testing.T) {
	cases := map[string]struct {
		clock scanClock
		ks    switchState
		want  string
	}{
		"market closed":     {clock: scanClock{open: false}, want: "market_closed"},
		"clock unavailable": {clock: scanClock{err: errors.New("down")}, want: "clock_unavailable"},
		"kill switch":       {clock: scanClock{open: true}, ks: switchState{engaged: true}, want: "kill_switch"},
		"kill switch error": {clock: scanClock{open: true}, ks: switchState{err: errors.New("db")}, want: "kill_switch"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			p := &scanProposer{}
			obs := &scanObserver{}
			sig := scanSignals{byKey: map[string]*ports.Signal{"AAPL/rsi": buy("AAPL", 0.9)}}
			res := newTestScanner(sig, tc.clock, tc.ks, p, obs).Scan(context.Background())
			if res.Skipped != tc.want || len(p.created) != 0 {
				t.Fatalf("got %+v, created %d", res, len(p.created))
			}
			if obs.scans != 1 {
				t.Fatalf("a skipped scan still counts as a scan (health metric): %+v", obs)
			}
		})
	}
}

func TestScannerStopsWhenRateLimited(t *testing.T) {
	sig := scanSignals{byKey: map[string]*ports.Signal{"AAPL/rsi": buy("AAPL", 0.8), "MSFT/rsi": buy("MSFT", 0.8)}}
	p := &scanProposer{err: usecases.ErrIntentRateLimited}
	newTestScanner(sig, scanClock{open: true}, switchState{}, p, &scanObserver{}).Scan(context.Background())
	if len(p.created) != 1 {
		t.Fatalf("scan should stop after the first rate-limited proposal, created %d", len(p.created))
	}
}

func TestScannerPolicyValidate(t *testing.T) {
	ok := usecases.ScannerPolicy{Interval: 5 * time.Minute, Symbols: []string{"SPY"}, Strategies: []string{"rsi"}}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []usecases.ScannerPolicy{
		{Interval: 30 * time.Second, Symbols: ok.Symbols, Strategies: ok.Strategies},
		{Interval: time.Minute, Strategies: ok.Strategies},
		{Interval: time.Minute, Symbols: ok.Symbols},
	} {
		if bad.Validate() == nil {
			t.Fatalf("expected error for %+v", bad)
		}
	}
}

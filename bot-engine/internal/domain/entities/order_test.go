package entities

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func TestNewMarketOrderValidation(t *testing.T) {
	one := decimal.NewFromInt(1)
	if _, err := NewMarketOrder("", OrderSideBuy, one, "", ""); err == nil {
		t.Fatal("empty symbol accepted")
	}
	if _, err := NewMarketOrder("AAPL", OrderSideBuy, decimal.Zero, "", ""); err == nil {
		t.Fatal("zero qty accepted")
	}
	if _, err := NewMarketOrder("AAPL", "hold", one, "", ""); err == nil {
		t.Fatal("bad side accepted")
	}
	if _, err := NewLimitOrder("AAPL", OrderSideBuy, one, decimal.Zero, "", ""); err == nil {
		t.Fatal("zero limit price accepted")
	}
	o, err := NewLimitOrder("AAPL", OrderSideSell, one, decimal.RequireFromString("10.01"), "", "")
	if err != nil || o.Type != OrderTypeLimit || o.Status != OrderStatusPending || o.ClientOrderID() != o.ID.String() {
		t.Fatalf("order = %+v, %v", o, err)
	}
}

func TestApplyBrokerStateIsImmutableAndRecordsFill(t *testing.T) {
	o, _ := NewMarketOrder("AAPL", OrderSideBuy, decimal.NewFromInt(2), "", "")
	price, at := decimal.RequireFromString("150.01"), time.Now().UTC()
	filled := o.ApplyBrokerState(BrokerState{
		BrokerID: "b-1", Status: OrderStatusFilled, FilledQty: decimal.NewFromInt(2), FilledAvgPrice: &price, FilledAt: &at,
	})
	if o.Status != OrderStatusPending || o.FilledPrice != nil || o.BrokerID != "" {
		t.Fatalf("original mutated: %+v", o)
	}
	if filled.Status != OrderStatusFilled || filled.BrokerID != "b-1" || !filled.FilledPrice.Equal(price) || !filled.IsTerminal() {
		t.Fatalf("filled = %+v", filled)
	}
	price = decimal.Zero // the order keeps its own copy
	if filled.FilledPrice.IsZero() {
		t.Fatal("fill price aliases the caller's value")
	}
}

func TestIsTerminal(t *testing.T) {
	for s, want := range map[OrderStatus]bool{
		OrderStatusPending: false, OrderStatusSubmitted: false, OrderStatusPartiallyFilled: false,
		OrderStatusFilled: true, OrderStatusCanceled: true, OrderStatusRejected: true, OrderStatusExpired: true,
	} {
		if got := (&Order{Status: s}).IsTerminal(); got != want {
			t.Errorf("%s: IsTerminal = %v, want %v", s, got, want)
		}
	}
}

func TestPositionUpdatePrice(t *testing.T) {
	p := Position{Symbol: "AAPL", Quantity: decimal.NewFromInt(-3), AverageEntryPrice: decimal.NewFromInt(200)}
	u := p.UpdatePrice(decimal.RequireFromString("190.10"))
	if !u.UnrealizedPnL.Equal(decimal.RequireFromString("29.7")) || !u.MarketValue.Equal(decimal.RequireFromString("-570.3")) {
		t.Fatalf("short position = %+v", u)
	}
}

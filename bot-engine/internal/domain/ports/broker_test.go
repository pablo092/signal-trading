package ports

import (
	"errors"
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/trading-bot/bot-engine/internal/domain/entities"
)

func TestSubmitOrderRequestValidate(t *testing.T) {
	one := decimal.NewFromInt(1)
	zero := decimal.Zero
	valid := SubmitOrderRequest{ClientOrderID: "c-1", Symbol: "AAPL", Side: entities.OrderSideBuy, Type: entities.OrderTypeMarket, Qty: one}
	cases := []struct {
		name   string
		mutate func(*SubmitOrderRequest)
		ok     bool
	}{
		{"valid market", func(*SubmitOrderRequest) {}, true},
		{"valid limit", func(r *SubmitOrderRequest) { r.Type, r.LimitPrice = entities.OrderTypeLimit, &one }, true},
		{"missing client order ID", func(r *SubmitOrderRequest) { r.ClientOrderID = "" }, false},
		{"client order ID too long", func(r *SubmitOrderRequest) { r.ClientOrderID = strings.Repeat("x", 129) }, false},
		{"missing symbol", func(r *SubmitOrderRequest) { r.Symbol = "" }, false},
		{"bad side", func(r *SubmitOrderRequest) { r.Side = "hold" }, false},
		{"zero qty", func(r *SubmitOrderRequest) { r.Qty = zero }, false},
		{"negative qty", func(r *SubmitOrderRequest) { r.Qty = one.Neg() }, false},
		{"market with limit price", func(r *SubmitOrderRequest) { r.LimitPrice = &one }, false},
		{"limit without price", func(r *SubmitOrderRequest) { r.Type = entities.OrderTypeLimit }, false},
		{"limit with zero price", func(r *SubmitOrderRequest) { r.Type, r.LimitPrice = entities.OrderTypeLimit, &zero }, false},
		{"unknown type", func(r *SubmitOrderRequest) { r.Type = "stop" }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := valid
			tc.mutate(&r)
			err := r.Validate()
			if tc.ok != (err == nil) {
				t.Fatalf("ok=%v, err=%v", tc.ok, err)
			}
			if err != nil && !errors.Is(err, ErrInvalidOrder) {
				t.Fatalf("error must wrap ErrInvalidOrder: %v", err)
			}
		})
	}
}

func TestSubmitRequestForUsesOrderIDAsClientOrderID(t *testing.T) {
	o, err := entities.NewMarketOrder("AAPL", entities.OrderSideBuy, decimal.NewFromInt(2), "rsi", "sig-1")
	if err != nil {
		t.Fatal(err)
	}
	r := SubmitRequestFor(o)
	if r.ClientOrderID != o.ID.String() || !r.Qty.Equal(o.Quantity) || r.Validate() != nil {
		t.Fatalf("request = %+v", r)
	}
}

package config

import (
	"strings"
	"testing"
)

const (
	supaSession = "postgres://postgres.abcd:pw-SECRET@aws-0-eu-west-1.pooler.supabase.com:5432/postgres"
	supaDirect  = "postgresql://postgres:pw-SECRET@db.abcd.supabase.co:5432/postgres"
)

func TestDatabaseValidation(t *testing.T) {
	cases := []struct {
		name, url, schema string
		ok                bool
	}{
		{"no database", "", "", true},
		{"local compose", "postgres://postgres:pw-SECRET@postgres:5432/tradingbot?sslmode=disable", "", true},
		{"local with schema", "postgres://postgres:pw-SECRET@postgres:5432/tradingbot", "tradingbot", true},
		{"supabase session pooler", supaSession + "?sslmode=require", "tradingbot", true},
		{"supabase direct verify-full", supaDirect + "?sslmode=verify-full", "tradingbot", true},
		{"supabase transaction pooler", strings.Replace(supaSession, ":5432", ":6543", 1) + "?sslmode=require", "tradingbot", false},
		{"supabase without TLS", supaSession, "tradingbot", false},
		{"supabase sslmode=prefer", supaSession + "?sslmode=prefer", "tradingbot", false},
		{"supabase public schema", supaSession + "?sslmode=require", "public", false},
		{"supabase no schema", supaSession + "?sslmode=require", "", false},
		{"bad schema name", "", "Trading-Bot", false},
		{"pg_ schema", "", "pg_engine", false},
		{"not a postgres url", "mysql://u:pw-SECRET@h/db", "", false},
		{"key=value dsn", "host=h user=u password=pw-SECRET", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Config{PaperTrading: true, AlpacaBaseURL: "https://paper-api.alpaca.markets", DatabaseURL: tc.url, DatabaseSchema: tc.schema}
			err := c.Validate()
			if (err == nil) != tc.ok {
				t.Fatalf("Validate() err=%v, want ok=%v", err, tc.ok)
			}
			if err != nil && strings.Contains(err.Error(), "pw-SECRET") {
				t.Fatalf("error leaks the password: %v", err)
			}
			if !tc.ok {
				if _, verified := c.TradingMode(); verified {
					t.Fatal("mode must not be verified when the database config is invalid")
				}
			}
		})
	}
}

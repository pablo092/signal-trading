package contracttest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Every canonical example validates, and the validator rejects known-bad documents
// (so a permissive validator cannot pass the producer tests by accident).
func TestExamplesConformToTheirSchemas(t *testing.T) {
	var manifest map[string]string
	if err := json.Unmarshal(Example(t, "manifest.json"), &manifest); err != nil {
		t.Fatal(err)
	}
	for example, schema := range manifest {
		t.Run(example, func(t *testing.T) { Validate(t, schema, Example(t, example)) })
	}
	entries, _ := os.ReadDir(filepath.Join(Dir(t), "examples"))
	if len(entries)-1 != len(manifest) {
		t.Fatalf("%d example files but %d manifest entries", len(entries)-1, len(manifest))
	}
}

func TestValidatorRejectsViolations(t *testing.T) {
	for name, tc := range map[string]struct{ schema, doc string }{
		"bad enum":           {"signal.schema.json", `{"id":"1","symbol":"AAPL","direction":"MAYBE","strength":"WEAK","price":1,"confidence":0.5,"reason":"","strategy_name":"x","is_actionable":true,"generated_at":"2026-11-25T00:00:00Z"}`},
		"missing required":   {"error.schema.json", `{"error":"x"}`},
		"extra property":     {"portfolio-equity.schema.json", `{"equity":1,"paper_mode":true,"market_open":false,"live":true}`},
		"wrong type":         {"portfolio-equity.schema.json", `{"equity":"1","paper_mode":true,"market_open":false}`},
		"bad date-time":      {"kill-switch.schema.json", `{"engaged":true,"forced":false,"reason":"","updated_by":"","updated_at":"yesterday"}`},
		"over maximum":       {"signal.schema.json", `{"id":"1","symbol":"AAPL","direction":"BUY","strength":"WEAK","price":1,"confidence":2,"reason":"","strategy_name":"x","is_actionable":true,"generated_at":"2026-11-25T00:00:00Z"}`},
		"type/data mismatch": {"ws-event.schema.json", `{"v":1,"type":"signal.received","id":"x","ts":"2026-11-25T00:00:00Z","data":{"engaged":true,"forced":false,"reason":"","updated_by":"","updated_at":"2026-11-25T00:00:00Z"}}`},
		"wrong version":      {"ws-event.schema.json", `{"v":2,"type":"kill_switch.changed","id":"x","ts":"2026-11-25T00:00:00Z","data":{"engaged":true,"forced":false,"reason":"","updated_by":"","updated_at":"2026-11-25T00:00:00Z"}}`},
		"array item":         {"portfolio-positions.schema.json", `[{"symbol":"","quantity":1,"average_entry_price":1,"current_price":1,"unrealized_pnl":0}]`},
	} {
		t.Run(name, func(t *testing.T) {
			if errs := Check(t, tc.schema, []byte(tc.doc)); len(errs) == 0 {
				t.Fatal("violation not detected")
			}
		})
	}
}

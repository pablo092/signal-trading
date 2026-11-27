package config

import (
	"os"
	"testing"
	"time"
)

func loadWithScanner(t *testing.T, enabled, interval string) (*Config, error) {
	t.Helper()
	clearRisk(t)
	t.Setenv("ALPACA_API_KEY", "test-key")
	t.Setenv("ALPACA_SECRET_KEY", "test-secret")
	for k, v := range map[string]string{"SCANNER_ENABLED": enabled, "SCANNER_INTERVAL": interval} {
		t.Setenv(k, v)
		if v == "" {
			os.Unsetenv(k)
		}
	}
	return Load()
}

func TestScannerIsOffByDefault(t *testing.T) {
	c, err := loadWithScanner(t, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if c.ScannerEnabled || c.ScannerInterval != 5*time.Minute {
		t.Fatalf("defaults: enabled=%v interval=%s", c.ScannerEnabled, c.ScannerInterval)
	}
}

func TestScannerSettings(t *testing.T) {
	c, err := loadWithScanner(t, "true", "2m")
	if err != nil {
		t.Fatal(err)
	}
	if !c.ScannerEnabled || c.ScannerInterval != 2*time.Minute {
		t.Fatalf("got enabled=%v interval=%s", c.ScannerEnabled, c.ScannerInterval)
	}
}

func TestScannerSettingsAreStrict(t *testing.T) {
	for _, tc := range []struct{ enabled, interval string }{
		{"yes", ""}, {"", "30s"}, {"", "2h"}, {"", "soon"},
	} {
		if _, err := loadWithScanner(t, tc.enabled, tc.interval); err == nil {
			t.Fatalf("SCANNER_ENABLED=%q SCANNER_INTERVAL=%q should fail", tc.enabled, tc.interval)
		}
	}
}

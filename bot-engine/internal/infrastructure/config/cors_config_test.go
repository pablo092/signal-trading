package config

import "testing"

func TestCORSOriginValidation(t *testing.T) {
	cases := []struct {
		name    string
		origins []string
		ok      bool
	}{
		{"none", nil, true},
		{"local app", []string{"http://localhost:5000"}, true},
		{"https with trailing slash", []string{"https://app.example.com/"}, true},
		{"several", []string{"http://localhost:5000", "http://127.0.0.1:5000"}, true},
		{"wildcard", []string{"*"}, false},
		{"wildcard subdomain", []string{"https://*.example.com"}, false},
		{"with path", []string{"https://app.example.com/demo"}, false},
		{"no scheme", []string{"localhost:5000"}, false},
		{"ftp scheme", []string{"ftp://app.example.com"}, false},
		{"credentials", []string{"https://u:p@app.example.com"}, false},
		{"query", []string{"https://app.example.com?x=1"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Config{PaperTrading: true, AlpacaBaseURL: "https://paper-api.alpaca.markets", CORSAllowedOrigins: tc.origins}
			if err := c.Validate(); (err == nil) != tc.ok {
				t.Fatalf("Validate() err=%v, want ok=%v", err, tc.ok)
			}
		})
	}
}

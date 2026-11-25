package auth

import (
	"strings"
	"testing"
)

func entry(id, typ, token string, scopes ...string) *Principal {
	return &Principal{ID: id, Type: typ, SHA256: HashToken(token), Scopes: scopes}
}

func TestAuthenticate(t *testing.T) {
	s, err := NewStore([]*Principal{
		entry("gw", ActorAgent, "agent-token", ScopeMarketRead, ScopePortfolioRead),
		entry("me", ActorHuman, "human-token", ScopePortfolioRead, ScopeOrdersPlaceLegacy),
	})
	if err != nil {
		t.Fatal(err)
	}
	if p := s.Authenticate("agent-token"); p == nil || p.ID != "gw" {
		t.Fatalf("expected gw, got %+v", p)
	}
	if p := s.Authenticate("human-token"); p == nil || p.ID != "me" {
		t.Fatalf("expected me, got %+v", p)
	}
	for _, bad := range []string{"", "agent-token ", "AGENT-TOKEN", HashToken("agent-token")} {
		if p := s.Authenticate(bad); p != nil {
			t.Fatalf("token %q must not authenticate", bad)
		}
	}
}

func TestEmptyStoreDeniesEverything(t *testing.T) {
	s, err := LoadStore("", "")
	if err != nil || s.Len() != 0 {
		t.Fatalf("err=%v len=%d", err, s.Len())
	}
	if s.Authenticate("anything") != nil {
		t.Fatal("empty store must deny")
	}
}

func TestOnlyHumansHoldHumanOnlyScopes(t *testing.T) {
	for _, typ := range []string{ActorAgent, ActorService} {
		for _, sc := range []string{ScopeIntentApprove, ScopeAdminKillswitch, ScopeOrdersPlaceLegacy} {
			_, err := NewStore([]*Principal{entry("gw", typ, "t", sc)})
			if err == nil {
				t.Fatalf("%s token with %s must be rejected at load", typ, sc)
			}
		}
		// Defense in depth at verification time, even if an entry bypassed NewStore.
		p := entry("gw", typ, "t", ScopeIntentApprove)
		p.digest = mustDigest(t, p.SHA256)
		s := &Store{principals: []*Principal{p}}
		if s.Authenticate("t") != nil {
			t.Fatalf("%s token with a human-only scope must not authenticate", typ)
		}
	}
	if _, err := NewStore([]*Principal{entry("me", ActorHuman, "t", ScopeIntentApprove)}); err != nil {
		t.Fatalf("a human may approve: %v", err)
	}
}

func mustDigest(t *testing.T, h string) []byte {
	t.Helper()
	s, err := NewStore([]*Principal{{ID: "x", Type: ActorHuman, SHA256: h, Scopes: []string{ScopeMarketRead}}})
	if err != nil {
		t.Fatal(err)
	}
	return s.principals[0].digest
}

func TestLoadStoreValidation(t *testing.T) {
	bad := []string{
		`not json`,
		`[{"id":"","type":"human","sha256":"` + HashToken("a") + `","scopes":["market:read"]}]`,
		`[{"id":"a","type":"robot","sha256":"` + HashToken("a") + `","scopes":["market:read"]}]`,
		`[{"id":"a","type":"human","sha256":"plaintext-token","scopes":["market:read"]}]`,
		`[{"id":"a","type":"human","sha256":"` + HashToken("a") + `","scopes":[]}]`,
		`[{"id":"a","type":"human","sha256":"` + HashToken("a") + `","scopes":["admin:*"]}]`,
		`[{"id":"a","type":"human","sha256":"` + HashToken("a") + `","scopes":["market:read"]},{"id":"a","type":"human","sha256":"` + HashToken("b") + `","scopes":["market:read"]}]`,
		`[{"id":"a","type":"human","sha256":"` + HashToken("a") + `","scopes":["market:read"]},{"id":"b","type":"human","sha256":"` + HashToken("a") + `","scopes":["market:read"]}]`,
	}
	for i, raw := range bad {
		if _, err := LoadStore(raw, ""); err == nil {
			t.Fatalf("case %d must fail: %s", i, raw)
		}
	}
	good := `[{"id":"gw","type":"agent","sha256":"` + strings.ToUpper(HashToken("a")) + `","scopes":["market:read","portfolio:read"]}]`
	s, err := LoadStore(good, "")
	if err != nil || s.Authenticate("a") == nil {
		t.Fatalf("good table must load and authenticate: %v", err)
	}
}

func TestLoadStoreErrorNeverEchoesInput(t *testing.T) {
	_, err := LoadStore(`{"secret-looking-value": 1`, "")
	if err == nil || strings.Contains(err.Error(), "secret-looking-value") {
		t.Fatalf("error must not echo the table content: %v", err)
	}
}

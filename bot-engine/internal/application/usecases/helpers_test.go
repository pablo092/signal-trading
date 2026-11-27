package usecases_test

import (
	"context"
	"sync"

	"github.com/trading-bot/bot-engine/internal/domain/ports"
)

// Shared manual mocks for the usecases tests (no generated code needed).
// The reconciler publishes and audits from its own goroutine while a test
// reads, so the mocks guard their state (go test -race).

type mockPublisher struct {
	mu        sync.Mutex
	published []ports.DomainEvent
}

func (m *mockPublisher) Publish(_ context.Context, e ports.DomainEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.published = append(m.published, e)
	return nil
}

func (m *mockPublisher) ofType(eventType string) []ports.DomainEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []ports.DomainEvent
	for _, e := range m.published {
		if e.Type == eventType {
			out = append(out, e)
		}
	}
	return out
}

// recordingAudit keeps events and can be made to fail: on every record
// (fail), or only on one action (failOn) so a later step can be targeted.
type recordingAudit struct {
	mu     sync.Mutex
	events []ports.AuditEvent
	fail   error
	failOn string
}

func (a *recordingAudit) Record(_ context.Context, e ports.AuditEvent) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.fail != nil && (a.failOn == "" || a.failOn == e.Action) {
		return a.fail
	}
	a.events = append(a.events, e)
	return nil
}

func (a *recordingAudit) actions() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]string, len(a.events))
	for i, e := range a.events {
		out[i] = e.Action
	}
	return out
}

// switchState is a fixed kill-switch reading.
type switchState struct {
	engaged bool
	err     error
}

func (s switchState) Engaged(context.Context) (bool, error) { return s.engaged, s.err }

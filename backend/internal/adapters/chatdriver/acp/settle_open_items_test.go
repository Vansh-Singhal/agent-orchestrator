package acp

import (
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// newSettleConversation builds a conversation holding one tool that never
// reported a terminal status, as happens when a turn's response is lost while a
// tool call is still in flight.
func newSettleConversation(t *testing.T, status acpsdk.ToolCallStatus) *conversation {
	t.Helper()
	conv := &conversation{
		activeTurn: "turn-1", events: make(chan ports.ChatEvent, 16),
		log: slog.New(slog.DiscardHandler),
		tools: map[string]*toolState{
			"tool-1": {id: "tool-1", title: "git push", status: status},
		},
		messages: map[string]string{}, thoughts: map[string]string{},
		nestedMessages: map[string]nestedMessageState{},
	}
	return conv
}

// settleToolEvents runs settleOpenItems and returns the activity events it
// produced for the outstanding tool.
func settleToolEvents(t *testing.T, conv *conversation, state domain.TurnState, class domain.ErrorClass) []ports.ChatEvent {
	t.Helper()
	conv.settleOpenItems("turn-1", state, class)
	close(conv.events)
	var events []ports.ChatEvent
	for event := range conv.events {
		if event.Kind == ports.ChatEventActivityCompleted {
			events = append(events, event)
		}
	}
	if len(events) != 1 {
		t.Fatalf("settleOpenItems emitted %d tool events, want 1", len(events))
	}
	return events
}

// A tool that was still in flight when the provider's response was lost did not
// provably fail. Marking it failed would tell a reader its effects never landed,
// when a push or file write may already have happened. Regression for #5967.
func TestAmbiguousFailureLeavesInFlightToolUnconfirmed(t *testing.T) {
	for _, status := range []acpsdk.ToolCallStatus{
		acpsdk.ToolCallStatusPending,
		acpsdk.ToolCallStatusInProgress,
	} {
		conv := newSettleConversation(t, status)
		events := settleToolEvents(t, conv, domain.TurnStateFailed, domain.ErrorClassAmbiguous)
		event := events[0]

		if event.ActivityStatus == domain.ActivityStatusFailed {
			t.Fatalf("tool status %v marked failed under an ambiguous failure; its outcome was never reported", status)
		}
		if !strings.Contains(event.Summary, "outcome unconfirmed") {
			t.Fatalf("summary = %q, want it to say the outcome is unconfirmed", event.Summary)
		}
	}
}

// An interrupted turn is a deliberate stop, so work that had not reported a
// terminal status genuinely did not complete and may be recorded as failed.
func TestInterruptedTurnStillFailsInFlightTool(t *testing.T) {
	conv := newSettleConversation(t, acpsdk.ToolCallStatusInProgress)
	events := settleToolEvents(t, conv, domain.TurnStateInterrupted, domain.ErrorClassUnknown)

	if got := events[0].ActivityStatus; got != domain.ActivityStatusFailed {
		t.Fatalf("interrupted turn left tool status %q, want %q", got, domain.ActivityStatusFailed)
	}
	if strings.Contains(events[0].Summary, "unconfirmed") {
		t.Fatalf("interrupted turn summary %q should not claim an unknown outcome", events[0].Summary)
	}
}

// A rejected request is a different fact from a lost response: nothing ran, so
// the tool cannot have half-completed and must not be described as unconfirmed.
func TestPermanentFailureDoesNotClaimUnconfirmedOutcome(t *testing.T) {
	conv := newSettleConversation(t, acpsdk.ToolCallStatusInProgress)
	events := settleToolEvents(t, conv, domain.TurnStateFailed, domain.ErrorClassPermanent)

	if strings.Contains(events[0].Summary, "unconfirmed") {
		t.Fatalf("permanent failure summary %q must not claim an unconfirmed outcome", events[0].Summary)
	}
	if !strings.Contains(events[0].Summary, "did not complete") {
		t.Fatalf("summary = %q, want it to say the tool did not complete", events[0].Summary)
	}
}

// A tool that already reported a terminal status is left alone. Settling an
// unsettled tool must not be a second path that rewrites a settled one.
func TestSettleLeavesAlreadyTerminalToolUntouched(t *testing.T) {
	for _, status := range []acpsdk.ToolCallStatus{
		acpsdk.ToolCallStatusCompleted,
		acpsdk.ToolCallStatusFailed,
	} {
		conv := newSettleConversation(t, status)
		conv.settleOpenItems("turn-1", domain.TurnStateFailed, domain.ErrorClassAmbiguous)
		close(conv.events)
		for event := range conv.events {
			if event.Kind == ports.ChatEventActivityCompleted {
				t.Fatalf("tool status %v was re-settled by a failed turn", status)
			}
		}
	}
}

// A recovered turn is terminal without a portable outcome, so its in-flight
// tools are still unconfirmed rather than failed.
func TestRecoveredTurnLeavesInFlightToolUnconfirmed(t *testing.T) {
	conv := newSettleConversation(t, acpsdk.ToolCallStatusInProgress)
	conv.settleOpenItems("turn-1", domain.TurnStateRecovered, domain.ErrorClassUnknown)
	close(conv.events)
	for event := range conv.events {
		if event.Kind != ports.ChatEventActivityCompleted {
			continue
		}
		if strings.Contains(event.Summary, "unconfirmed") {
			return
		}
	}
	t.Fatal("recovered turn did not mark its in-flight tool unconfirmed")
}

// A completed turn that never reported a terminal status for a tool it started
// leaves that tool spinning forever with no explanation, because only the
// failed and recovered cases were annotated. The turn's own outcome is known,
// so the honest wording is that the tool did not complete, not that the outcome
// is unconfirmed.
func TestCompletedTurnAnnotatesInFlightTool(t *testing.T) {
	// A class is included because a completed turn's known outcome must win over
	// whatever classification rode along, or the tool would read as unconfirmed.
	for _, class := range []domain.ErrorClass{"", domain.ErrorClassTransient, domain.ErrorClassUnknown} {
		for _, status := range []acpsdk.ToolCallStatus{
			acpsdk.ToolCallStatusPending,
			acpsdk.ToolCallStatusInProgress,
		} {
			conv := newSettleConversation(t, status)
			events := settleToolEvents(t, conv, domain.TurnStateCompleted, class)

			if events[0].ActivityStatus == domain.ActivityStatusFailed {
				t.Fatalf("tool status %v marked failed under a completed turn", status)
			}
			if !strings.Contains(events[0].Summary, "did not complete") {
				t.Fatalf("class %q summary = %q, want it to say the tool did not complete", class, events[0].Summary)
			}
			if strings.Contains(events[0].Summary, "unconfirmed") {
				t.Fatalf("class %q summary = %q, want a known-outcome turn to avoid claiming unconfirmed", class, events[0].Summary)
			}
		}
	}
}

// The unsettled summary must survive into the activity detail the timeline
// renders, so a reader can see the tool existed and what it was doing.
func TestUnsettledToolKeepsItsIdentity(t *testing.T) {
	conv := newSettleConversation(t, acpsdk.ToolCallStatusInProgress)
	events := settleToolEvents(t, conv, domain.TurnStateFailed, domain.ErrorClassAmbiguous)

	if events[0].ProviderItemID == "" {
		t.Fatal("unsettled tool lost its provider item id")
	}
	var detail map[string]any
	if err := json.Unmarshal(events[0].Detail, &detail); err != nil {
		t.Fatalf("unsettled tool detail is not JSON: %v", err)
	}
	if detail["toolKind"] == nil {
		t.Fatalf("unsettled tool detail lost its tool kind: %#v", detail)
	}
}

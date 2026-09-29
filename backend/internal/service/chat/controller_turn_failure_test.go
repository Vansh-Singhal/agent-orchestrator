package chat_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

// runFailingPrimaryTurn drives a session to the point where its single turn fails
// with the given error and class, then waits for the turn to settle as failed.
func runFailingPrimaryTurn(
	t *testing.T,
	h *harness,
	cause error,
	class domain.ErrorClass,
) {
	t.Helper()
	ctx := context.Background()

	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "root", ClientMessageID: "c1", Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatalf("send root: %v", err)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return turnStateByText(t, s)["root"] == domain.TurnStateRunning
	})

	h.conv.emit(ports.ChatEvent{
		Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1",
		TurnState: domain.TurnStateFailed, Err: cause, ErrorClass: class,
	})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return turnStateByText(t, s)["root"] == domain.TurnStateFailed
	})
	// The activity signal is emitted after the turn is settled, so give the
	// controller a moment to project it.
	time.Sleep(50 * time.Millisecond)
}

// lastActivitySignal returns the most recent signal the controller reported.
func lastActivitySignal(t *testing.T, h *harness) ports.ActivitySignal {
	t.Helper()
	signals := h.activity.snapshot()
	if len(signals) == 0 {
		t.Fatal("no activity signal was reported")
	}
	return signals[len(signals)-1]
}

// A turn that failed must not be reported as a completed, idle turn. Reporting it
// that way made a worker that died mid-turn indistinguishable from one that
// finished, so the failure was invisible and the session looked healthy. Regression
// for issue #5967.
func TestFailedPrimaryTurnReportsFailureNotCompletion(t *testing.T) {
	h := newHarness(t)
	runFailingPrimaryTurn(t, h, errors.New("gateway timeout"), domain.ErrorClassAmbiguous)

	signal := lastActivitySignal(t, h)
	if signal.State != domain.ActivityWaitingInput {
		t.Fatalf("failed turn reported state %q, want %q so the session asks for attention",
			signal.State, domain.ActivityWaitingInput)
	}
	if signal.Event != "chat.turn.failed" {
		t.Fatalf("failed turn reported event %q, want %q", signal.Event, "chat.turn.failed")
	}
}

// A failed turn must not be reported as ActivityBlocked. That state is reserved
// for an agent parked on a permission or approval dialog, where injecting input
// would answer the dialog for the user. Reusing it for a dead turn suppressed
// delivery and the confirm nudge, so the failure's one remedy, switching
// harnesses, was the thing that stopped working. Both states still render as
// needs_input.
func TestFailedTurnDoesNotClaimAPendingPermissionDialog(t *testing.T) {
	h := newHarness(t)
	runFailingPrimaryTurn(t, h, errors.New("gateway timeout"), domain.ErrorClassAmbiguous)

	if got := lastActivitySignal(t, h).State; got == domain.ActivityBlocked {
		t.Fatalf("failed turn reported %q, which is reserved for permission dialogs", got)
	}
	if !lastActivitySignal(t, h).State.NeedsInput() {
		t.Fatalf("failed turn reported %q, want a state that still needs the user", lastActivitySignal(t, h).State)
	}
}

// An ambiguous failure is the case that matters most: the provider may have run
// the work and lost the response, so the session must not be left looking like it
// simply finished.
func TestAmbiguousTurnFailureStillRequestsAttention(t *testing.T) {
	h := newHarness(t)
	runFailingPrimaryTurn(t, h, errors.New("stream disconnected"), domain.ErrorClassAmbiguous)

	if got := lastActivitySignal(t, h).State; got != domain.ActivityWaitingInput {
		t.Fatalf("ambiguous failure reported %q, want %q", got, domain.ActivityWaitingInput)
	}
}

// A permanent failure needs attention for the same reason, and the distinction
// from a transient one is recorded on the turn rather than the activity state.
func TestPermanentTurnFailureIsDistinguishedFromTransient(t *testing.T) {
	for _, tc := range []struct {
		name        string
		class       domain.ErrorClass
		wantMessage string
	}{
		{"permanent", domain.ErrorClassPermanent, "model not supported"},
		{"transient", domain.ErrorClassTransient, "rate limited"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			runFailingPrimaryTurn(t, h, errors.New(tc.wantMessage), tc.class)

			if got := lastActivitySignal(t, h).State; got != domain.ActivityWaitingInput {
				t.Fatalf("%s failure reported %q, want %q", tc.name, got, domain.ActivityWaitingInput)
			}

			// The class is durable on the turn, so a later reader can tell a
			// retryable failure from one that will fail again.
			snapshot, err := h.st.LoadConversationSnapshot(context.Background(), h.ctrl.ConversationID())
			if err != nil {
				t.Fatalf("load snapshot: %v", err)
			}
			turn := turnByText(t, snapshot, "root")
			if turn.ErrorClass != tc.class {
				t.Fatalf("turn error class = %q, want %q", turn.ErrorClass, tc.class)
			}
		})
	}
}

// A completed turn is the only outcome that reports idle under
// chat.turn.completed. This is the case the failed-turn fix must not regress.
func TestCompletedPrimaryTurnStillReportsIdleCompletion(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "root", ClientMessageID: "c1", Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatalf("send root: %v", err)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return turnStateByText(t, s)["root"] == domain.TurnStateRunning
	})
	h.conv.emit(ports.ChatEvent{
		Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1",
		TurnState: domain.TurnStateCompleted,
	})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return turnStateByText(t, s)["root"] == domain.TurnStateCompleted
	})
	time.Sleep(50 * time.Millisecond)

	signal := lastActivitySignal(t, h)
	if signal.State != domain.ActivityIdle || signal.Event != "chat.turn.completed" {
		t.Fatalf("completed turn reported (%q, %q), want (%q, %q)",
			signal.State, signal.Event, domain.ActivityIdle, "chat.turn.completed")
	}
}

// An interrupted turn is the user's own doing, not a failure, so it keeps the
// existing idle/completed projection.
func TestInterruptedPrimaryTurnIsNotTreatedAsFailure(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "root", ClientMessageID: "c1", Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatalf("send root: %v", err)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return turnStateByText(t, s)["root"] == domain.TurnStateRunning
	})
	h.conv.emit(ports.ChatEvent{
		Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1",
		TurnState: domain.TurnStateInterrupted,
	})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return turnStateByText(t, s)["root"] == domain.TurnStateInterrupted
	})
	time.Sleep(50 * time.Millisecond)

	if got := lastActivitySignal(t, h).State; got != domain.ActivityIdle {
		t.Fatalf("interrupted turn reported %q, want %q", got, domain.ActivityIdle)
	}
}

// An auxiliary (nested/sub-agent) turn failure must not claim the session's
// activity state. Only the turn AO dispatched itself speaks for the session.
func TestNonPrimaryTurnFailureDoesNotClaimSessionActivity(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "root", ClientMessageID: "c1", Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatalf("send root: %v", err)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return turnStateByText(t, s)["root"] == domain.TurnStateRunning
	})
	before := len(h.activity.snapshot())

	// A different provider turn id while one is pending is auxiliary work.
	h.conv.emit(ports.ChatEvent{
		Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-2",
		TurnState: domain.TurnStateFailed, Err: errors.New("boom"),
		ErrorClass: domain.ErrorClassTransient,
	})
	time.Sleep(100 * time.Millisecond)

	if got := len(h.activity.snapshot()); got != before {
		t.Fatalf("auxiliary failed turn emitted %d new activity signals, want 0", got-before)
	}
}

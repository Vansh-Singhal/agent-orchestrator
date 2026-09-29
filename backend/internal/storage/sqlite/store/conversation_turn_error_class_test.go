package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// The stored error class is what lets a later reader tell a retryable blip from a
// turn whose work may already have landed. Losing it on the way to disk would
// leave every failure indistinguishable again, which is the bug in #5967.
func TestSettleTurnPersistsErrorClass(t *testing.T) {
	for _, tc := range []struct {
		name    string
		state   domain.TurnState
		class   domain.ErrorClass
		want    domain.ErrorClass
		message string
	}{
		{"ambiguous failure", domain.TurnStateFailed, domain.ErrorClassAmbiguous, domain.ErrorClassAmbiguous, "gateway timeout"},
		{"transient failure", domain.TurnStateFailed, domain.ErrorClassTransient, domain.ErrorClassTransient, "rate limited"},
		{"permanent failure", domain.TurnStateFailed, domain.ErrorClassPermanent, domain.ErrorClassPermanent, "model not found"},
		// A failure AO could not classify must not be stored as permanent, or a
		// client could offer a repeat that runs the agent's work twice.
		{"unclassified failure", domain.TurnStateFailed, domain.ErrorClassUnknown, domain.ErrorClassUnknown, "who knows"},
		// A class a future provider invents degrades to the cautious default.
		{"future failure class", domain.TurnStateFailed, domain.ErrorClass("from-the-future"), domain.ErrorClassAmbiguous, "new failure"},
		// A successful turn has no cause to classify.
		{"completed turn", domain.TurnStateCompleted, domain.ErrorClassTransient, "", ""},
		// An interrupted turn is the user's stop, not a failure.
		{"interrupted turn", domain.TurnStateInterrupted, domain.ErrorClassTransient, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, sessionID, conversationID := conversationFixture(t)
			ctx := context.Background()
			turnID := "turn-" + tc.name
			created, err := s.AppendUserMessage(ctx, conversationID, sessionID, "gen-1", domain.ConversationMessage{
				ID: "msg-" + tc.name, Role: domain.MessageRoleUser, Text: tc.name,
				Origin: domain.MessageOriginHuman,
			}, turnID, histClock)
			if err != nil || !created {
				t.Fatalf("append user message: created=%v err=%v", created, err)
			}
			if err := s.BindTurnToProvider(ctx, turnID, "provider-"+tc.name, histClock); err != nil {
				t.Fatalf("bind turn: %v", err)
			}
			if err := s.SettleTurn(ctx, conversationID, "provider-"+tc.name,
				tc.state, tc.message, tc.class, histClock.Add(time.Minute)); err != nil {
				t.Fatalf("settle turn: %v", err)
			}

			snapshot, err := s.LoadConversationSnapshot(ctx, conversationID)
			if err != nil {
				t.Fatalf("load snapshot: %v", err)
			}
			var found bool
			for _, turn := range snapshot.Turns {
				if turn.ID != turnID {
					continue
				}
				found = true
				if turn.State != tc.state {
					t.Fatalf("state = %q, want %q", turn.State, tc.state)
				}
				if turn.ErrorClass != tc.want {
					t.Fatalf("error class = %q, want %q", turn.ErrorClass, tc.want)
				}
			}
			if !found {
				t.Fatalf("turn %q missing from snapshot", turnID)
			}
		})
	}
}

// A turn that is still running has no class yet. Settling must not require the
// caller to supply one it does not have.
func TestRunningTurnCarriesNoErrorClass(t *testing.T) {
	s, sessionID, conversationID := conversationFixture(t)
	ctx := context.Background()
	created, err := s.AppendUserMessage(ctx, conversationID, sessionID, "gen-1", domain.ConversationMessage{
		ID: "msg-running", Role: domain.MessageRoleUser, Text: "running",
		Origin: domain.MessageOriginHuman,
	}, "turn-running", histClock)
	if err != nil || !created {
		t.Fatalf("append user message: created=%v err=%v", created, err)
	}
	if err := s.BindTurnToProvider(ctx, "turn-running", "provider-running", histClock); err != nil {
		t.Fatalf("bind turn: %v", err)
	}

	snapshot, err := s.LoadConversationSnapshot(ctx, conversationID)
	if err != nil {
		t.Fatalf("load snapshot: %v", err)
	}
	for _, turn := range snapshot.Turns {
		if turn.ID != "turn-running" {
			continue
		}
		if turn.ErrorClass != "" {
			t.Fatalf("running turn has error class %q, want none", turn.ErrorClass)
		}
		return
	}
	t.Fatal("running turn missing from snapshot")
}

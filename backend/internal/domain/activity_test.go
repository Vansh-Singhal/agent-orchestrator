package domain

import "testing"

// TestActivityState_StickyAndNeedsInput pins the two independent state
// families: sticky states must survive the passage of time, and needs-input
// states are those where the user is the unblocker (waiting_input = awaiting
// the next instruction, blocked = pending permission/approval decision).
func TestActivityState_StickyAndNeedsInput(t *testing.T) {
	tests := []struct {
		state      ActivityState
		sticky     bool
		needsInput bool
	}{
		{ActivityActive, false, false},
		{ActivityIdle, false, false},
		{ActivityWaitingInput, true, true},
		{ActivityBlocked, true, true},
		{ActivityExited, false, false},
	}
	for _, tt := range tests {
		t.Run(string(tt.state), func(t *testing.T) {
			if got := tt.state.IsSticky(); got != tt.sticky {
				t.Errorf("IsSticky() = %v, want %v", got, tt.sticky)
			}
			if got := tt.state.NeedsInput(); got != tt.needsInput {
				t.Errorf("NeedsInput() = %v, want %v", got, tt.needsInput)
			}
		})
	}
}

// IsQuiescent answers a third question: is this session safe to hand to another
// controller or harness? Idle and both paused states are, because none of them
// is mid-turn. Active is not, because its work is genuinely in flight, and
// exited is not, because the terminal is gone and there is nothing to hand over.
func TestActivityState_IsQuiescent(t *testing.T) {
	tests := []struct {
		state     ActivityState
		quiescent bool
	}{
		{ActivityIdle, true},
		{ActivityWaitingInput, true},
		{ActivityBlocked, true},
		{ActivityActive, false},
		{ActivityExited, false},
	}
	for _, tt := range tests {
		t.Run(string(tt.state), func(t *testing.T) {
			if got := tt.state.IsQuiescent(); got != tt.quiescent {
				t.Errorf("IsQuiescent() = %v, want %v", got, tt.quiescent)
			}
		})
	}
}

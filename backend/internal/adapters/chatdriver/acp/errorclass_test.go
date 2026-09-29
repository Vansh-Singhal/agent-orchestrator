package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// providerDataError builds the shape an ACP agent relays: the provider's own
// structured error nested under "providerData", which is where the real code and
// type live.
func providerDataError(t *testing.T, code int, kind string) *acpsdk.RequestError {
	t.Helper()
	inner := map[string]any{"message": "upstream failure"}
	if code != 0 {
		inner["code"] = code
	}
	if kind != "" {
		inner["type"] = kind
	}
	raw, err := json.Marshal(map[string]any{"providerData": inner})
	if err != nil {
		t.Fatalf("marshal provider data: %v", err)
	}
	return &acpsdk.RequestError{
		Code:    rpcInternalError,
		Message: "Internal error",
		Data:    json.RawMessage(raw),
	}
}

func TestClassifyACPError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want domain.ErrorClass
	}{
		{
			// The reported case: OpenCode returned a 504 whose response was lost.
			// The request was accepted, so the agent's work may have run.
			name: "provider 504 is ambiguous",
			err:  providerDataError(t, 504, ""),
			want: domain.ErrorClassAmbiguous,
		},
		{
			name: "provider 500 is ambiguous",
			err:  providerDataError(t, 500, ""),
			want: domain.ErrorClassAmbiguous,
		},
		{
			name: "provider 503 is transient",
			err:  providerDataError(t, 503, ""),
			want: domain.ErrorClassTransient,
		},
		{
			name: "provider 429 is transient",
			err:  providerDataError(t, 429, ""),
			want: domain.ErrorClassTransient,
		},
		{
			name: "provider overload 529 is transient",
			err:  providerDataError(t, 529, ""),
			want: domain.ErrorClassTransient,
		},
		{
			name: "provider 400 is a rejection",
			err:  providerDataError(t, 400, ""),
			want: domain.ErrorClassPermanent,
		},
		{
			name: "provider 401 is a rejection",
			err:  providerDataError(t, 401, ""),
			want: domain.ErrorClassPermanent,
		},
		{
			name: "timeout type is ambiguous",
			err:  providerDataError(t, 0, "timeout"),
			want: domain.ErrorClassAmbiguous,
		},
		{
			name: "overloaded type is ambiguous",
			err:  providerDataError(t, 0, "overloaded"),
			want: domain.ErrorClassAmbiguous,
		},
		{
			name: "rate limit type is transient",
			err:  providerDataError(t, 0, "rate_limit"),
			want: domain.ErrorClassTransient,
		},
		{
			name: "invalid request type is permanent",
			err:  providerDataError(t, 0, "invalid_request"),
			want: domain.ErrorClassPermanent,
		},
		{
			name: "authentication error type is permanent",
			err:  providerDataError(t, 0, "authentication_error"),
			want: domain.ErrorClassPermanent,
		},
		{
			// The ACP auth code means the provider refused the credential, so the
			// turn demonstrably never ran.
			name: "acp auth required is permanent",
			err:  &acpsdk.RequestError{Code: rpcAuthRequired, Message: "authentication required"},
			want: domain.ErrorClassPermanent,
		},
		{
			// A missing optional method is not a failure of the work.
			name: "method not found is not a failure",
			err:  &acpsdk.RequestError{Code: rpcMethodNotFound, Message: "no such method"},
			want: domain.ErrorClassUnknown,
		},
		{
			name: "bare internal error is ambiguous",
			err:  &acpsdk.RequestError{Code: rpcInternalError, Message: "Internal error"},
			want: domain.ErrorClassAmbiguous,
		},
		{
			// The agent returned an internal error whose payload says nothing
			// about the cause. The request was still accepted, so the work may
			// have run: ambiguous, never permanent.
			name: "unreadable provider error is not permanent",
			err:  &acpsdk.RequestError{Code: rpcInternalError, Message: "something went wrong", Data: map[string]any{"note": "x"}},
			want: domain.ErrorClassAmbiguous,
		},
		{
			name: "our own cancellation is ambiguous",
			err:  context.Canceled,
			want: domain.ErrorClassAmbiguous,
		},
		{
			name: "our own deadline is ambiguous",
			err:  context.DeadlineExceeded,
			want: domain.ErrorClassAmbiguous,
		},
		{
			name:    "nil error has no cause",
			err:     nil,
			want:    domain.ErrorClassUnknown,
		},
		{
			name: "plain non-acp error is ambiguous",
			err:  fmt.Errorf("dial tcp: connection reset"),
			want: domain.ErrorClassAmbiguous,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyACPError(tc.err); got != tc.want {
				t.Fatalf("classifyACPError(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}

// A class AO does not recognize must never read as a definite failure, or a
// client offering "retry" could run the agent's work twice. The zero value and a
// value a future provider invents both have to degrade toward caution.
func TestUnknownClassIsNotTreatedAsDefiniteFailure(t *testing.T) {
	for _, class := range []domain.ErrorClass{
		domain.ErrorClassUnknown,
		domain.ErrorClass("something-new"),
		domain.ErrorClass(""),
	} {
		if !class.OutcomeUnknown() {
			t.Fatalf("class %q reports a known outcome; an unrecognized cause must be treated as unknown", class)
		}
	}
}

func TestErrorClassRetryable(t *testing.T) {
	retryable := map[domain.ErrorClass]bool{
		domain.ErrorClassTransient: true,
		domain.ErrorClassAmbiguous: false,
		domain.ErrorClassPermanent: false,
		domain.ErrorClassUnknown:   false,
	}
	for class, want := range retryable {
		if got := class.Retryable(); got != want {
			t.Fatalf("%q.Retryable() = %v, want %v", class, got, want)
		}
	}
}

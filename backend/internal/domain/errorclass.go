package domain

// ErrorClass is what AO knows about a failed turn's cause, and specifically
// whether the agent's work may already have landed.
//
// A provider 504 or dropped stream is not the same fact as a rejected
// credential. The first leaves the outcome unknown: the request reached the
// provider, so a tool call that was in flight may have completed, committed, or
// pushed before the response was lost. The second proves nothing ran. Collapsing
// both into "failed" (issue #5967) is what let a timed-out worker stall silently,
// because no consumer could tell a recoverable blip from a dead session.
type ErrorClass string

// Error classes. The zero value is ErrorClassUnknown: an unclassified failure
// is treated as ambiguous rather than permanent, so nothing concludes a task did
// not run on the strength of an error AO did not understand.
const (
	// ErrorClassUnknown means the provider gave no usable classification. The
	// work may or may not have run.
	ErrorClassUnknown ErrorClass = "unknown"
	// ErrorClassAmbiguous means the request reached the provider but the outcome
	// was never reported (timeout, upstream idle timeout, stream loss). Work may
	// have partially or fully landed and must not be assumed not to have run.
	ErrorClassAmbiguous ErrorClass = "ambiguous"
	// ErrorClassTransient means a temporary provider-side condition. Retrying the
	// same turn is reasonable; the previous attempt's work is not trustworthy.
	ErrorClassTransient ErrorClass = "transient"
	// ErrorClassPermanent means the provider rejected the request outright, such
	// as a refused credential, a missing model, or a quota refusal. Nothing ran.
	ErrorClassPermanent ErrorClass = "permanent"
)

// Retryable reports whether re-sending the same turn could plausibly succeed
// without any external change. Ambiguous failures are deliberately excluded:
// their work may already have landed, so a blind retry can double-execute.
func (c ErrorClass) Retryable() bool {
	return c == ErrorClassTransient
}

// OutcomeUnknown reports whether the agent's work may have taken effect despite
// the failure. Consumers that could repeat side effects (re-delegating a task,
// spawning a replacement worker) must not assume a failed turn did nothing.
//
// Any class other than a known-definite one answers true, including a value a
// future provider invents. Treating an unrecognized class as a known outcome is
// the exact failure this guards against, so the answer degrades toward caution.
func (c ErrorClass) OutcomeUnknown() bool {
	switch c {
	case ErrorClassAmbiguous, ErrorClassUnknown:
		return true
	case ErrorClassTransient, ErrorClassPermanent:
		return false
	default:
		return true
	}
}

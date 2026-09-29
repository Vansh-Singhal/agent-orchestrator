package acp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// JSON-RPC codes AO treats as classifiable causes rather than opaque failures.
const (
	rpcAuthRequired  = -32000
	rpcMethodNotFound = -32601
	rpcInternalError = -32603
)

// classifyACPError reports what AO can honestly conclude about a failed
// session/prompt (issue #5967).
//
// The distinction that matters is not "which error text appeared" but "may the
// agent's work already have landed." A provider 504 means the request reached
// the provider and the response was lost, so a tool call that was mid-flight
// when the stream dropped may have committed, or pushed, or written a file. AO
// must not let that read as a clean failure, because a consumer that re-runs the
// task would then execute it twice.
//
// Every branch below is driven by a structured field the provider actually
// sent: a JSON-RPC code, or the nested code/metadata an agent relays. An error
// AO cannot classify is ambiguous, not permanent, so nothing downstream infers
// "this definitely did not run" from an error it did not understand.
func classifyACPError(err error) domain.ErrorClass {
	if err == nil {
		// No error, no cause to classify. Callers only reach this on a terminal
		// event that did not fail; naming the constant beats a bare "".
		return domain.ErrorClassUnknown
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		// Our own context ended, so the provider's reply is genuinely unknown.
		return domain.ErrorClassAmbiguous
	}

	var requestErr *acpsdk.RequestError
	if !errors.As(err, &requestErr) {
		return domain.ErrorClassAmbiguous
	}

	switch requestErr.Code {
	case rpcAuthRequired:
		// The provider refused the credential, so the turn never ran.
		return domain.ErrorClassPermanent
	case rpcMethodNotFound:
		// An optional protocol call the agent does not implement. Not a failure
		// of the work; the driver already treats it as skippable.
		return domain.ErrorClassUnknown
	}

	if code, ok := providerErrorCode(requestErr.Data); ok {
		switch {
		// Provider-side rate limiting and capacity errors are temporary. The
		// turn did not complete, and re-sending it is the provider's own
		// recommendation.
		case code == 429, code == 529, code == 503:
			return domain.ErrorClassTransient
		// 5xx gateway families where the response was lost after the request was
		// accepted. Work may have run.
		case code >= 500 && code <= 504:
			return domain.ErrorClassAmbiguous
		// 4xx other than 429 is a rejection: the provider refused the request.
		case code >= 400 && code <= 499:
			return domain.ErrorClassPermanent
		}
	}

	if kind, ok := providerErrorType(requestErr.Data); ok {
		switch kind {
		case "timeout", "overloaded", "capacity", "server_error":
			return domain.ErrorClassAmbiguous
		case "rate_limit", "rate_limit_exceeded", "usage_limit_reached":
			return domain.ErrorClassTransient
		case "invalid_request", "authentication_error", "permission_error",
			"not_found_error", "invalid_api_key":
			return domain.ErrorClassPermanent
		}
	}

	// -32603 is the SDK's catch-all for a handler error the agent did not shape
	// itself. It carries no cause, so the work may or may not have run.
	if requestErr.Code == rpcInternalError {
		return domain.ErrorClassAmbiguous
	}
	return domain.ErrorClassUnknown
}

// providerErrorCode digs the provider's own HTTP-shaped status out of a
// JSON-RPC error's data payload. The ACP relay wraps opaque provider data under
// "providerData" and may add its own keys beside it, so both the wrapper and a
// bare payload are accepted.
func providerErrorCode(data any) (int, bool) {
	for _, candidate := range errorPayloads(data) {
		if raw, ok := candidate["code"]; ok {
			var code int
			if err := json.Unmarshal(raw, &code); err == nil {
				return code, true
			}
			// Some providers send the code as a string.
			var text string
			if err := json.Unmarshal(raw, &text); err == nil {
				if code, ok := parseInt(text); ok {
					return code, true
				}
			}
		}
	}
	return 0, false
}

// providerErrorType reads the provider's machine-readable error_type, which
// survives cases where the numeric code is too generic to act on.
func providerErrorType(data any) (string, bool) {
	for _, candidate := range errorPayloads(data) {
		for _, key := range []string{"error_type", "errorType", "type"} {
			raw, ok := candidate[key]
			if !ok {
				continue
			}
			var value string
			if err := json.Unmarshal(raw, &value); err != nil {
				continue
			}
			if value = strings.ToLower(strings.TrimSpace(value)); value != "" {
				return value, true
			}
		}
		metadata, ok := candidate["metadata"]
		if !ok {
			continue
		}
		if nested, ok := metadataObject(metadata); ok {
			if raw, ok := nested["error_type"]; ok {
				var value string
				if err := json.Unmarshal(raw, &value); err == nil {
					if value = strings.ToLower(strings.TrimSpace(value)); value != "" {
						return value, true
					}
				}
			}
		}
	}
	return "", false
}

// errorPayloads returns the JSON objects worth inspecting inside a JSON-RPC
// error's data, most specific first. A relay-wrapped payload is unwrapped so
// classification sees the provider's own fields rather than AO's envelope.
func errorPayloads(data any) []map[string]json.RawMessage {
	if data == nil {
		return nil
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return nil
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &top); err != nil {
		return nil
	}
	payloads := make([]map[string]json.RawMessage, 0, 3)
	if raw, ok := top["providerData"]; ok {
		if nested, ok := objectValue(raw); ok {
			payloads = append(payloads, nested)
		}
	}
	payloads = append(payloads, top)
	// A provider may nest its error one level deeper under "error".
	if raw, ok := top["error"]; ok {
		if nested, ok := objectValue(raw); ok {
			payloads = append(payloads, nested)
		}
	}
	return payloads
}

func objectValue(raw json.RawMessage) (map[string]json.RawMessage, bool) {
	trimmed := strings.TrimSpace(string(raw))
	if !strings.HasPrefix(trimmed, "{") {
		return nil, false
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, false
	}
	return object, true
}

func metadataObject(raw json.RawMessage) (map[string]json.RawMessage, bool) {
	return objectValue(raw)
}

func parseInt(value string) (int, bool) {
	parsed := 0
	if value == "" {
		return 0, false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return 0, false
		}
		parsed = parsed*10 + int(r-'0')
	}
	return parsed, true
}

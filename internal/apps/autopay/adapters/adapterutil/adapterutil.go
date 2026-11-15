// Package adapterutil holds the small helpers every gateway adapter needs: digging into untyped webhook JSON,
// building dedupe keys, and classifying transport errors.
package adapterutil

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"strings"

	"go-backend/internal/apps/autopay/gateway"
	"go-backend/pkg/utils"
)

// Dig walks nested JSON objects and returns the string at the path ("" when missing or not a string).
func Dig(m map[string]interface{}, path ...string) string {
	if s, ok := DigAny(m, path...).(string); ok {
		return s
	}
	return ""
}

// DigAny walks nested JSON objects and returns the raw value at the path (nil when missing).
func DigAny(m map[string]interface{}, path ...string) interface{} {
	var cur interface{} = m
	for _, key := range path {
		obj, ok := cur.(map[string]interface{})
		if !ok {
			return nil
		}
		cur = obj[key]
	}
	return cur
}

// DigNumber returns the JSON number at the path.
func DigNumber(m map[string]interface{}, path ...string) (float64, bool) {
	f, ok := DigAny(m, path...).(float64)
	return f, ok
}

// HashKey builds a stable dedupe key from the identifying parts of an event (the parts are joined with a separator
// that cannot occur in ids, so different part boundaries never collide).
func HashKey(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x1f")))
	return hex.EncodeToString(sum[:])
}

// IsTimeoutOrReset reports transport failures after which a request may or may not have been processed (the request
// was sent, but no usable response came back).
func IsTimeoutOrReset(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return true
	}
	msg := strings.ToLower(err.Error())
	for _, s := range []string{"timeout", "connection reset", "eof", "broken pipe", "client.timeout"} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

// Rejected is a permanent business rejection raised by the adapter itself (before or instead of a gateway call).
func Rejected(code, message string) error {
	return gateway.NewError(gateway.ErrorKindRejectedPermanently, code, message, nil)
}

// FailureCode picks the first non-empty of the gateway's failure code / reason, else fallback, within the 255
// characters autopay_charge_attempts.failure_code holds.
func FailureCode(code, reason, fallback string) string {
	return utils.Truncate(utils.FirstNonEmpty(code, reason, fallback), 255)
}

// TransportError classifies a failure that is not an API answer (no usable response came back). On a call that changes
// state at the gateway a timeout or reset leaves the outcome unknown (the request may have been processed); anything
// else, and any failure of a read-only call, is transient.
func TransportError(err error, mutating bool) error {
	if mutating && IsTimeoutOrReset(err) {
		return gateway.NewError(gateway.ErrorKindOutcomeUnknown, "transport", err.Error(), err)
	}
	return gateway.NewError(gateway.ErrorKindTransient, "transport", err.Error(), err)
}

// CancelAlreadyDone settles a cancel the gateway rejected (rejection is the classified error, cause the raw one): a
// gateway that documents no code for "this mandate is not cancellable" is asked for the mandate's state instead. A
// mandate that is already cancelled or expired (by the customer, by us, or because it ended), or that was never
// authorized (the customer abandoned the checkout), has nothing live to cancel: that is reported as already-processed,
// which the core treats as success and then cancels locally. Anything else, or a failed lookup, returns the rejection.
func CancelAlreadyDone(rejection, cause error, fetchState func() (gateway.MandateState, error)) error {
	if !gateway.IsKind(rejection, gateway.ErrorKindRejectedPermanently) {
		return rejection // an outage or an unknown outcome is not a rejection: no extra lookup
	}
	state, err := fetchState()
	if err != nil {
		return rejection
	}
	switch state {
	case gateway.MandateCancelled, gateway.MandateExpired, gateway.MandateAwaitingAuthorization:
		return gateway.NewError(gateway.ErrorKindAlreadyProcessed, "nothing_to_cancel", "the mandate is "+string(state)+": nothing live to cancel", cause)
	}
	return rejection
}

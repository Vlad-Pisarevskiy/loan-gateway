package dispatch

import "testing"

func TestOutcomeKindString(t *testing.T) {
	want := map[OutcomeKind]string{
		OutcomeDecision: "decision",
		OutcomeAccepted: "accepted",
		OutcomeRetry:    "retry",
		OutcomeFailed:   "failed",
		0:               "unknown",
	}
	for kind, name := range want {
		if got := kind.String(); got != name {
			t.Errorf("%d.String() = %q, want %q", int(kind), got, name)
		}
	}
}

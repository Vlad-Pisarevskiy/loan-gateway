package application

import "testing"

func TestRequestStatusFinal(t *testing.T) {
	want := map[RequestStatus]bool{
		RequestQueued:          false,
		RequestSending:         false,
		RequestWaitingCallback: false,
		RequestApproved:        true,
		RequestRejected:        true,
		RequestFailed:          true,
	}
	for status, final := range want {
		if got := status.Final(); got != final {
			t.Errorf("%s.Final() = %v, want %v", status, got, final)
		}
	}
}

func TestViewStatus(t *testing.T) {
	v := View{Banks: []BankResult{{Status: RequestApproved}, {Status: RequestWaitingCallback}}}
	if got := v.Status(); got != StatusProcessing {
		t.Fatalf("status = %s, want %s", got, StatusProcessing)
	}
	v.Banks[1].Status = RequestFailed
	if got := v.Status(); got != StatusCompleted {
		t.Fatalf("status = %s, want %s", got, StatusCompleted)
	}
}

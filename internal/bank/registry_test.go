package bank

import (
	"context"
	"slices"
	"testing"
)

type stubBank string

func (b stubBank) Code() string { return string(b) }

func (stubBank) Submit(context.Context, SubmitRequest) (SubmitResult, error) {
	return SubmitResult{}, nil
}

func TestRegistry(t *testing.T) {
	reg, err := NewRegistry(stubBank("westbank"), stubBank("northbank"))
	if err != nil {
		t.Fatal(err)
	}
	if got := reg.Codes(); !slices.Equal(got, []string{"northbank", "westbank"}) {
		t.Fatalf("codes = %v, want sorted", got)
	}
	if b, ok := reg.Get("northbank"); !ok || b.Code() != "northbank" {
		t.Fatalf("Get(northbank) = %v, %v", b, ok)
	}
	if _, ok := reg.Get("eastbank"); ok {
		t.Fatal("Get(eastbank) must report a missing bank")
	}
}

func TestRegistryRejectsDuplicatesAndEmpty(t *testing.T) {
	if _, err := NewRegistry(stubBank("northbank"), stubBank("northbank")); err == nil {
		t.Fatal("duplicate codes must be rejected")
	}
	if _, err := NewRegistry(); err == nil {
		t.Fatal("empty registry must be rejected")
	}
}

package main

import (
	"log/slog"
	"testing"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/config"
)

func TestNewHandlerSelectsDialect(t *testing.T) {
	log := slog.New(slog.DiscardHandler)
	if h, err := newHandler(t.Context(), config.Mockbank{Dialect: "north"}, log); err != nil || h == nil {
		t.Fatalf("north: handler %v, err %v", h, err)
	}
	if _, err := newHandler(t.Context(), config.Mockbank{Dialect: "south"}, log); err == nil {
		t.Fatal("unknown dialect must fail")
	}
	west := config.Mockbank{Dialect: "west", CallbackURL: "http://api/v1/webhooks/banks/westbank", CallbackSecret: "s"}
	if h, err := newHandler(t.Context(), west, log); err != nil || h == nil {
		t.Fatalf("west: handler %v, err %v", h, err)
	}
}

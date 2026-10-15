package main

import (
	"net/http"
	"testing"
)

func TestAPIServerLimitsSlowClients(t *testing.T) {
	srv := newAPIServer(":0", http.NotFoundHandler())
	if srv.ReadHeaderTimeout <= 0 || srv.ReadTimeout <= 0 || srv.IdleTimeout <= 0 {
		t.Fatalf("timeouts: header %s, read %s, idle %s; want all set: a slow body or idle keep-alive holds a goroutine forever",
			srv.ReadHeaderTimeout, srv.ReadTimeout, srv.IdleTimeout)
	}
}

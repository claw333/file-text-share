package main

import (
	"net/http"
	"testing"
	"time"
)

func TestHTTPServerTimeoutsAreBounded(t *testing.T) {
	server := newHTTPServer(config{addr: "127.0.0.1:0"}, http.NotFoundHandler())

	if server.ReadTimeout > 15*time.Minute {
		t.Fatalf("ReadTimeout = %s, want <= 15m", server.ReadTimeout)
	}
	if server.WriteTimeout > 15*time.Minute {
		t.Fatalf("WriteTimeout = %s, want <= 15m", server.WriteTimeout)
	}
	if server.ReadHeaderTimeout > 10*time.Second {
		t.Fatalf("ReadHeaderTimeout = %s, want <= 10s", server.ReadHeaderTimeout)
	}
	if server.IdleTimeout > 90*time.Second {
		t.Fatalf("IdleTimeout = %s, want <= 90s", server.IdleTimeout)
	}
}

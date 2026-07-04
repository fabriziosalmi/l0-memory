package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestReachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if !reachable(srv.URL, time.Second) {
		t.Errorf("expected live server %s to be reachable", srv.URL)
	}
	// An unused port on loopback should not be reachable.
	if reachable("http://127.0.0.1:1/health", 500*time.Millisecond) {
		t.Errorf("expected 127.0.0.1:1 to be unreachable")
	}
}

package server

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"
)

func TestListenServesAndShutsDown(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "h.sock")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /ping", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]bool{"ok": true})
	})

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- Listen(ctx, sockPath, mux) }()

	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := net.Dial("unix", sockPath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("socket never appeared")
		}
		time.Sleep(20 * time.Millisecond)
	}

	client := &http.Client{Transport: &http.Transport{
		DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
			return net.Dial("unix", sockPath)
		},
	}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://unix/ping", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	var body map[string]bool
	_ = json.NewDecoder(resp.Body).Decode(&body)
	_ = resp.Body.Close()
	if !body["ok"] {
		t.Errorf("body = %v", body)
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("Listen returned %v", err)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("Listen did not return after cancel")
	}
	if _, err := net.Dial("unix", sockPath); err == nil {
		t.Error("socket still present after shutdown")
	}
}

func TestListenRemovesStaleSocket(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "h.sock")

	l1, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("seed listen: %v", err)
	}
	_ = l1.Close()

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- Listen(ctx, sockPath, http.NewServeMux()) }()

	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := net.Dial("unix", sockPath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("socket never bound (stale not cleared)")
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-errCh
}

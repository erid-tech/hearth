package agent_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	contractsagent "github.com/rocky-hq/contracts/go/agent"

	"github.com/rocky-hq/hearth/internal/agent"
)

func TestNopEmitter_Silent(t *testing.T) {
	t.Parallel()
	var e agent.Emitter = agent.NopEmitter{}
	e.Emit(context.Background(), contractsagent.AgentHatchEvent{
		Kind:    contractsagent.AgentRegistered,
		AgentID: "x-driver-y",
	})
	// No assertions — the point is that it does not panic and does not
	// require network configuration.
}

// waiter is a tiny helper that lets tests block until the emitter's
// background goroutine has finished its POST.
type waiter struct {
	wg   sync.WaitGroup
	body atomic.Pointer[[]byte]
	hdr  atomic.Pointer[http.Header]
	code atomic.Int32
}

func TestHTTPEmitter_PostsEventJSON(t *testing.T) {
	t.Parallel()
	w := &waiter{}
	w.wg.Add(1)
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		defer w.wg.Done()
		buf, _ := io.ReadAll(r.Body)
		copy := append([]byte(nil), buf...)
		w.body.Store(&copy)
		h := r.Header.Clone()
		w.hdr.Store(&h)
		w.code.Store(int32(http.StatusAccepted))
		rw.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	e := agent.NewHTTPEmitter(srv.URL, 500*time.Millisecond, logger)

	ev := contractsagent.AgentHatchEvent{
		Kind:    contractsagent.AgentRegistered,
		AgentID: "iris-hq-driver-local-docker",
		Actor:   "",
		Ts:      time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC),
	}
	e.Emit(context.Background(), ev)

	waitOrFail(t, &w.wg, time.Second)

	if got := w.code.Load(); got != http.StatusAccepted {
		t.Errorf("server saw status %d, wanted 202", got)
	}
	hdr := *w.hdr.Load()
	if hdr.Get("Content-Type") != "application/json" {
		t.Errorf("content-type = %q", hdr.Get("Content-Type"))
	}
	if hdr.Get("x-rocky-user-role") != "operator" {
		t.Errorf("x-rocky-user-role = %q", hdr.Get("x-rocky-user-role"))
	}

	var back contractsagent.AgentHatchEvent
	if err := json.Unmarshal(*w.body.Load(), &back); err != nil {
		t.Fatalf("unmarshal server body: %v", err)
	}
	if back.Kind != contractsagent.AgentRegistered {
		t.Errorf("kind = %q", back.Kind)
	}
	if back.AgentID != ev.AgentID {
		t.Errorf("agent_id = %q", back.AgentID)
	}
}

func TestHTTPEmitter_Non2xxLoggedNotFatal(t *testing.T) {
	t.Parallel()
	var seen atomic.Int32
	var wg sync.WaitGroup
	wg.Add(1)
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		defer wg.Done()
		seen.Add(1)
		rw.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	e := agent.NewHTTPEmitter(srv.URL, 500*time.Millisecond, slog.New(slog.NewTextHandler(io.Discard, nil)))
	e.Emit(context.Background(), contractsagent.AgentHatchEvent{Kind: contractsagent.AgentInvoked, AgentID: "x-driver-y"})
	waitOrFail(t, &wg, time.Second)

	if seen.Load() != 1 {
		t.Errorf("server saw %d requests, want 1", seen.Load())
	}
	// Test passes when Emit did not panic and did not block — the
	// non-2xx is a log-only concern.
}

func TestEmitterFromEnv_UnsetReturnsNop(t *testing.T) {
	// Not parallel: mutates env.
	t.Setenv(agent.EnvHatchURL, "")
	e := agent.EmitterFromEnv()
	if _, ok := e.(agent.NopEmitter); !ok {
		t.Fatalf("EmitterFromEnv (unset URL) = %T, want NopEmitter", e)
	}
}

func TestEmitterFromEnv_SetReturnsHTTPEmitter(t *testing.T) {
	// Not parallel: mutates env.
	t.Setenv(agent.EnvHatchURL, "http://127.0.0.1:0/agent")
	t.Setenv(agent.EnvHatchTimeoutMS, "750")
	e := agent.EmitterFromEnv()
	he, ok := e.(*agent.HTTPEmitter)
	if !ok {
		t.Fatalf("EmitterFromEnv (URL set) = %T, want *HTTPEmitter", e)
	}
	if he.URL != "http://127.0.0.1:0/agent" {
		t.Errorf("URL = %q", he.URL)
	}
	if he.Timeout != 750*time.Millisecond {
		t.Errorf("Timeout = %v", he.Timeout)
	}
}

// ensure slog default is initialized without pulling in a real backend.
func init() {
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	_ = os.Stderr
}

func waitOrFail(t *testing.T, wg *sync.WaitGroup, timeout time.Duration) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		t.Fatalf("waited %v for emitter POST — server never received it", timeout)
	}
}

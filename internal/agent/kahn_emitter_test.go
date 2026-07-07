package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSlugTokenEnvKey(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"iris-hq": "KAHN_INGEST_TOKEN_IRIS_HQ",
		"foo.bar": "KAHN_INGEST_TOKEN_FOO_BAR",
		"simple":  "KAHN_INGEST_TOKEN_SIMPLE",
	}
	for slug, want := range cases {
		if got := SlugTokenEnvKey(slug); got != want {
			t.Errorf("SlugTokenEnvKey(%q) = %q, want %q", slug, got, want)
		}
	}
}

// mockKAHN records POSTs to /v1/ingest/agent-transitions.
type mockKAHN struct {
	mu      sync.Mutex
	calls   []mockCall
	status  int
	arrived chan struct{}
}

type mockCall struct {
	auth string
	body []byte
}

func newMockKAHN(status int) (*httptest.Server, *mockKAHN) {
	m := &mockKAHN{status: status, arrived: make(chan struct{}, 16)}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/ingest/agent-transitions" {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		m.mu.Lock()
		m.calls = append(m.calls, mockCall{auth: r.Header.Get("Authorization"), body: body})
		m.mu.Unlock()
		select {
		case m.arrived <- struct{}{}:
		default:
		}
		w.WriteHeader(m.status)
	}))
	return srv, m
}

func (m *mockKAHN) waitFor(t *testing.T, n int) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		m.mu.Lock()
		got := len(m.calls)
		m.mu.Unlock()
		if got >= n {
			return
		}
		select {
		case <-m.arrived:
		case <-deadline:
			t.Fatalf("timed out waiting for %d calls, got %d", n, got)
		}
	}
}

func TestKAHNEmitterPerWorkspaceTokenWins(t *testing.T) {
	t.Parallel()
	srv, mock := newMockKAHN(200)
	defer srv.Close()
	env := map[string]string{
		"KAHN_INGEST_TOKEN":         "global-tok",
		"KAHN_INGEST_TOKEN_IRIS_HQ": "per-ws-tok",
	}
	e := NewKAHNEmitter(srv.URL, time.Second, nil)
	e.Env = func(k string) string { return env[k] }
	e.EmitRunStart(context.Background(), "iris-hq", "iris-hq-driver-local-docker", "run-1", "provision")
	mock.waitFor(t, 1)
	if got := mock.calls[0].auth; got != "Bearer per-ws-tok" {
		t.Errorf("Authorization = %q, want per-workspace token", got)
	}
}

func TestKAHNEmitterFallsBackToGlobalToken(t *testing.T) {
	t.Parallel()
	srv, mock := newMockKAHN(200)
	defer srv.Close()
	env := map[string]string{"KAHN_INGEST_TOKEN": "global-tok"}
	e := NewKAHNEmitter(srv.URL, time.Second, nil)
	e.Env = func(k string) string { return env[k] }
	e.EmitRunStart(context.Background(), "iris-hq", "iris-hq-driver-local-docker", "run-2", "status")
	mock.waitFor(t, 1)
	if got := mock.calls[0].auth; got != "Bearer global-tok" {
		t.Errorf("Authorization = %q, want global token", got)
	}
}

func TestKAHNEmitterSkipsWhenNoTokenResolves(t *testing.T) {
	t.Parallel()
	srv, mock := newMockKAHN(200)
	defer srv.Close()
	env := map[string]string{}
	e := NewKAHNEmitter(srv.URL, time.Second, nil)
	e.Env = func(k string) string { return env[k] }
	e.EmitRunStart(context.Background(), "iris-hq", "iris-hq-driver-local-docker", "run-3", "provision")
	// Give any errant goroutine a beat to (mistakenly) POST.
	time.Sleep(50 * time.Millisecond)
	mock.mu.Lock()
	defer mock.mu.Unlock()
	if len(mock.calls) != 0 {
		t.Errorf("expected 0 calls, got %d", len(mock.calls))
	}
}

func TestKAHNEmitterSkipsWhenSlugEmpty(t *testing.T) {
	t.Parallel()
	srv, mock := newMockKAHN(200)
	defer srv.Close()
	env := map[string]string{"KAHN_INGEST_TOKEN": "tok"}
	e := NewKAHNEmitter(srv.URL, time.Second, nil)
	e.Env = func(k string) string { return env[k] }
	e.EmitRunStart(context.Background(), "", "some-agent", "run-4", "provision")
	time.Sleep(50 * time.Millisecond)
	mock.mu.Lock()
	defer mock.mu.Unlock()
	if len(mock.calls) != 0 {
		t.Errorf("expected 0 calls on empty slug, got %d", len(mock.calls))
	}
}

func TestKAHNEmitterNilReceiverIsNoop(t *testing.T) {
	t.Parallel()
	var e *KAHNEmitter
	// Must not panic.
	e.EmitRunStart(context.Background(), "iris-hq", "a", "r", "t")
	e.EmitRunEnd(context.Background(), "iris-hq", "a", "r", "converged", 0.1)
}

func TestKAHNEmitterRunStartShape(t *testing.T) {
	t.Parallel()
	srv, mock := newMockKAHN(200)
	defer srv.Close()
	env := map[string]string{"KAHN_INGEST_TOKEN_IRIS_HQ": "tok"}
	e := NewKAHNEmitter(srv.URL, time.Second, nil)
	e.Env = func(k string) string { return env[k] }
	e.EmitRunStart(context.Background(), "iris-hq", "iris-hq-driver-local-docker", "inv-uuid", "provision")
	mock.waitFor(t, 1)
	var got AgentRunStart
	if err := json.Unmarshal(mock.calls[0].body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Event != "agent_run_start" {
		t.Errorf("event = %q", got.Event)
	}
	if got.RunID != "inv-uuid" {
		t.Errorf("run_id = %q", got.RunID)
	}
	if got.AgentID != "iris-hq-driver-local-docker" {
		t.Errorf("agent_id = %q", got.AgentID)
	}
	if got.Task != "provision" {
		t.Errorf("task = %q", got.Task)
	}
	if !strings.HasSuffix(got.TS, "Z") || len(got.TS) != 24 {
		t.Errorf("ts shape = %q, want YYYY-MM-DDTHH:MM:SS.mmmZ", got.TS)
	}
}

func TestKAHNEmitterRunEndOutcomeAndDuration(t *testing.T) {
	t.Parallel()
	srv, mock := newMockKAHN(200)
	defer srv.Close()
	env := map[string]string{"KAHN_INGEST_TOKEN_IRIS_HQ": "tok"}
	e := NewKAHNEmitter(srv.URL, time.Second, nil)
	e.Env = func(k string) string { return env[k] }
	e.EmitRunEnd(context.Background(), "iris-hq", "iris-hq-driver-local-docker", "inv-uuid", "aborted", 0.42)
	mock.waitFor(t, 1)
	var got AgentRunEnd
	if err := json.Unmarshal(mock.calls[0].body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Event != "agent_run_end" {
		t.Errorf("event = %q", got.Event)
	}
	if got.Outcome != "aborted" {
		t.Errorf("outcome = %q", got.Outcome)
	}
	if got.TotalDurationS != 0.42 {
		t.Errorf("total_duration_s = %v", got.TotalDurationS)
	}
	// Required KAHN fields present (zero values are valid per schema).
	if got.TotalSteps != 0 || got.TotalToolCalls != 0 {
		t.Errorf("expected zero substep counts, got steps=%d tools=%d", got.TotalSteps, got.TotalToolCalls)
	}
}

func TestKAHNEmitterStripsTrailingSlashOnURL(t *testing.T) {
	t.Parallel()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/ingest/agent-transitions" {
			hits.Add(1)
			w.WriteHeader(200)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	env := map[string]string{"KAHN_INGEST_TOKEN": "tok"}
	e := NewKAHNEmitter(srv.URL+"/", time.Second, nil)
	e.Env = func(k string) string { return env[k] }
	e.EmitRunStart(context.Background(), "iris-hq", "a", "r", "t")
	deadline := time.Now().Add(2 * time.Second)
	for hits.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if hits.Load() != 1 {
		t.Fatalf("expected 1 hit on canonical path, got %d", hits.Load())
	}
}

func TestKAHNEmitterFromEnvUnsetURLReturnsNil(t *testing.T) {
	t.Setenv("KAHN_INGEST_URL", "")
	if e := KAHNEmitterFromEnv(); e != nil {
		t.Errorf("expected nil emitter with URL unset, got %+v", e)
	}
}

func TestKAHNEmitterFromEnvHonorsTimeoutS(t *testing.T) {
	t.Setenv("KAHN_INGEST_URL", "http://example.invalid")
	t.Setenv("KAHN_INGEST_TIMEOUT_S", "0.5")
	e := KAHNEmitterFromEnv()
	if e == nil {
		t.Fatal("expected non-nil emitter")
	}
	if e.Timeout != 500*time.Millisecond {
		t.Errorf("timeout = %v, want 500ms", e.Timeout)
	}
}

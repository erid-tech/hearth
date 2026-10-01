package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	contractsagent "github.com/erid-tech/contracts/go/agent"
)

// Emitter is the producer-side sink for AgentHatchEvent values.
// Implementations MUST be safe for concurrent use. Emit MUST NOT block
// the caller on network I/O — HTTPEmitter dispatches on a goroutine
// with a bounded timeout.
type Emitter interface {
	Emit(ctx context.Context, ev contractsagent.AgentHatchEvent)
}

// NopEmitter drops every event. The default when
// HEARTH_AGENT_HATCH_URL is unset.
type NopEmitter struct{}

func (NopEmitter) Emit(context.Context, contractsagent.AgentHatchEvent) {}

// HTTPEmitter POSTs each event to URL as JSON. Non-blocking: each
// Emit fires a goroutine bounded by Timeout. Non-2xx responses and
// transport errors are logged via slog at Warn level; the caller
// never observes them.
type HTTPEmitter struct {
	URL     string
	Client  *http.Client
	Timeout time.Duration
	Log     *slog.Logger
}

// NewHTTPEmitter constructs an HTTPEmitter with sane defaults.
// timeout <= 0 falls back to DefaultTimeout. logger nil falls back to
// slog.Default.
func NewHTTPEmitter(url string, timeout time.Duration, logger *slog.Logger) *HTTPEmitter {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &HTTPEmitter{
		URL:     url,
		Client:  &http.Client{Timeout: timeout},
		Timeout: timeout,
		Log:     logger,
	}
}

// DefaultTimeout is the wall-clock cap on a single Emit's HTTP round trip.
const DefaultTimeout = 2 * time.Second

func (e *HTTPEmitter) Emit(_ context.Context, ev contractsagent.AgentHatchEvent) {
	go e.doEmit(ev)
}

func (e *HTTPEmitter) doEmit(ev contractsagent.AgentHatchEvent) {
	body, err := json.Marshal(ev)
	if err != nil {
		e.Log.Warn("agent-emit: marshal failed", "err", err, "kind", ev.Kind, "agent_id", ev.AgentID)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), e.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.URL, bytes.NewReader(body))
	if err != nil {
		e.Log.Warn("agent-emit: request build failed", "err", err, "kind", ev.Kind, "agent_id", ev.AgentID)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	// Service identity: hearth speaks as the operator role. Phase 7c
	// hardens this via bearer auth against the relay-agent route.
	req.Header.Set("x-rocky-user-role", "operator")
	resp, err := e.Client.Do(req)
	if err != nil {
		e.Log.Warn("agent-emit: post failed", "err", err, "url", e.URL, "kind", ev.Kind, "agent_id", ev.AgentID)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		e.Log.Warn("agent-emit: non-2xx", "status", resp.StatusCode, "url", e.URL, "kind", ev.Kind, "agent_id", ev.AgentID)
	}
}

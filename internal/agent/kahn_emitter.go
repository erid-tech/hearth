// KAHN agent-transition emitter (Phase P1a-4).
//
// Sibling to Emitter (which ships agent-registration.v1 hatch events to
// the console SS-08 wrapper). KAHNEmitter ships KAHN Scope's agent-shape
// stream — `agent_run_start` + `agent_run_end` per driver RPC verb call —
// directly to KAHN Cloud's ingest endpoint. Same north-star signal the
// console shipper (P1a-3b) and ralph AgentJournal (P1a-3c) already emit.
//
// One KAHN run per RPC verb invocation. HEARTH has no substep grain to
// project, so the run is trivial: run_start immediately followed by
// run_end with total_steps=0. Producer-side agent identity mirrors the
// hatch agent_id: `<workspace_slug>-driver-<driver-name>`. run_id
// derives from the RPC invocation_id (uuid) so the KAHN run correlates
// 1:1 with the sibling agent.registered/invoked/completed triple.
//
// Fail-open: transport errors + non-2xx responses log at slog.Warn and
// are dropped. A KAHN outage MUST NOT fault an RPC.
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	EnvKAHNIngestURL      = "KAHN_INGEST_URL"
	EnvKAHNIngestToken    = "KAHN_INGEST_TOKEN"
	EnvKAHNIngestTimeoutS = "KAHN_INGEST_TIMEOUT_S"
	EnvKAHNDebug          = "KAHN_DEBUG"
	kahnIngestPath        = "/v1/ingest/agent-transitions"
	DefaultKAHNTimeout    = 5 * time.Second
	kahnTSFormat          = "2006-01-02T15:04:05.000Z"
)

// KAHNEmitter is the KAHN-Cloud producer for hearth. Nil-receiver is a
// no-op: unset KAHN_INGEST_URL yields a nil emitter which silently
// skips every ship, matching the Nop pattern used elsewhere.
type KAHNEmitter struct {
	URL     string
	Client  *http.Client
	Timeout time.Duration
	Log     *slog.Logger
	// Env source for token resolution. Nil → os.Getenv.
	Env func(string) string
}

// NewKAHNEmitter constructs a KAHNEmitter with sane defaults. Nil
// logger falls back to slog.Default. Timeout <= 0 falls back to
// DefaultKAHNTimeout.
func NewKAHNEmitter(url string, timeout time.Duration, logger *slog.Logger) *KAHNEmitter {
	if timeout <= 0 {
		timeout = DefaultKAHNTimeout
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &KAHNEmitter{
		URL:     url,
		Client:  &http.Client{Timeout: timeout},
		Timeout: timeout,
		Log:     logger,
	}
}

// KAHNEmitterFromEnv builds the emitter at boot. Returns nil when
// KAHN_INGEST_URL is unset — the operator has not opted in.
func KAHNEmitterFromEnv() *KAHNEmitter {
	url := os.Getenv(EnvKAHNIngestURL)
	if url == "" {
		return nil
	}
	timeout := DefaultKAHNTimeout
	if raw := os.Getenv(EnvKAHNIngestTimeoutS); raw != "" {
		if n, err := strconv.ParseFloat(raw, 64); err == nil && n > 0 {
			timeout = time.Duration(n * float64(time.Second))
		}
	}
	return NewKAHNEmitter(url, timeout, nil)
}

// SlugTokenEnvKey mirrors the console shipper (P1a-3b) and ralph
// AgentJournal (P1a-3c) convention: slug uppercased with '-'/'.'
// mapped to '_'. Example: iris-hq → KAHN_INGEST_TOKEN_IRIS_HQ.
func SlugTokenEnvKey(workspaceSlug string) string {
	upper := strings.ToUpper(workspaceSlug)
	upper = strings.ReplaceAll(upper, "-", "_")
	upper = strings.ReplaceAll(upper, ".", "_")
	return "KAHN_INGEST_TOKEN_" + upper
}

func (e *KAHNEmitter) env(key string) string {
	if e.Env != nil {
		return e.Env(key)
	}
	return os.Getenv(key)
}

// resolveToken picks the right Bearer at ship-time. Per-workspace
// KAHN_INGEST_TOKEN_<SLUG_UPPER> takes precedence over the global
// KAHN_INGEST_TOKEN fallback. Empty return → skip ship (matches the
// console + ralph rule — KAHN 401s on missing Bearer, wasted round
// trip).
func (e *KAHNEmitter) resolveToken(workspaceSlug string) string {
	if workspaceSlug != "" {
		if v := strings.TrimSpace(e.env(SlugTokenEnvKey(workspaceSlug))); v != "" {
			return v
		}
	}
	return strings.TrimSpace(e.env(EnvKAHNIngestToken))
}

// AgentRunStart / AgentRunEnd are the trimmed KAHN wire shapes hearth
// produces. total_steps=0 / total_tool_calls=0 because hearth has no
// substep grain for a single RPC verb call.
type AgentRunStart struct {
	TS      string `json:"ts"`
	RunID   string `json:"run_id"`
	Event   string `json:"event"` // always "agent_run_start"
	AgentID string `json:"agent_id"`
	Task    string `json:"task"`
	Model   string `json:"model,omitempty"`
}

type AgentRunEnd struct {
	TS                    string  `json:"ts"`
	RunID                 string  `json:"run_id"`
	Event                 string  `json:"event"` // always "agent_run_end"
	AgentID               string  `json:"agent_id"`
	Outcome               string  `json:"outcome"` // converged | aborted
	TotalSteps            int     `json:"total_steps"`
	TotalToolCalls        int     `json:"total_tool_calls"`
	TotalAuditCheckpoints int     `json:"total_audit_checkpoints"`
	AuditsPassed          int     `json:"audits_passed"`
	AuditsFailed          int     `json:"audits_failed"`
	TotalDurationS        float64 `json:"total_duration_s"`
}

// EmitRunStart / EmitRunEnd are fire-and-forget. Nil receiver is a
// no-op. Empty workspaceSlug also skips (mirrors the console + ralph
// rule — no owner, no tenancy, no emission).
func (e *KAHNEmitter) EmitRunStart(_ context.Context, workspaceSlug, agentID, runID, task string) {
	if e == nil || workspaceSlug == "" {
		return
	}
	ev := AgentRunStart{
		TS:      time.Now().UTC().Format(kahnTSFormat),
		RunID:   runID,
		Event:   "agent_run_start",
		AgentID: agentID,
		Task:    truncateForKAHN(task, 8192),
	}
	e.dispatch(workspaceSlug, ev)
}

func (e *KAHNEmitter) EmitRunEnd(_ context.Context, workspaceSlug, agentID, runID, outcome string, durationS float64) {
	if e == nil || workspaceSlug == "" {
		return
	}
	ev := AgentRunEnd{
		TS:             time.Now().UTC().Format(kahnTSFormat),
		RunID:          runID,
		Event:          "agent_run_end",
		AgentID:        agentID,
		Outcome:        outcome,
		TotalDurationS: durationS,
	}
	e.dispatch(workspaceSlug, ev)
}

func (e *KAHNEmitter) dispatch(workspaceSlug string, ev any) {
	token := e.resolveToken(workspaceSlug)
	if token == "" {
		if strings.TrimSpace(e.env(EnvKAHNDebug)) == "1" {
			e.Log.Info(
				"kahn-agent/emitter: no token resolved, skipping ship",
				"workspace_slug", workspaceSlug,
			)
		}
		return
	}
	go e.doShip(workspaceSlug, token, ev)
}

func (e *KAHNEmitter) doShip(workspaceSlug, token string, ev any) {
	body, err := json.Marshal(ev)
	if err != nil {
		e.Log.Warn("kahn-agent/emitter: marshal failed", "err", err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), e.Timeout)
	defer cancel()
	url := strings.TrimRight(e.URL, "/") + kahnIngestPath
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		e.Log.Warn("kahn-agent/emitter: request build failed", "err", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := e.Client.Do(req)
	if err != nil {
		e.Log.Warn("kahn-agent/emitter: post failed", "err", err, "url", url)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		e.Log.Warn("kahn-agent/emitter: non-2xx", "status", resp.StatusCode, "url", url)
		return
	}
	if strings.TrimSpace(e.env(EnvKAHNDebug)) == "1" {
		evMeta := kahnEventMeta(ev)
		e.Log.Info(
			"kahn-agent/emitter: ship complete",
			"workspace_slug", workspaceSlug,
			"event", evMeta.event,
			"run_id", evMeta.runID,
			"status", resp.StatusCode,
		)
	}
}

type kahnMeta struct {
	event string
	runID string
}

func kahnEventMeta(ev any) kahnMeta {
	switch v := ev.(type) {
	case AgentRunStart:
		return kahnMeta{event: v.Event, runID: v.RunID}
	case AgentRunEnd:
		return kahnMeta{event: v.Event, runID: v.RunID}
	default:
		return kahnMeta{event: fmt.Sprintf("%T", v)}
	}
}

func truncateForKAHN(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}

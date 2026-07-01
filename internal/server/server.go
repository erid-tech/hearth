// Package server implements the JSON-over-HTTP RPC layer in front of
// any driver.Driver. Phase 5c bind is a Unix socket; Phase 6 adds TCP
// + bearer auth.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	contractsagent "github.com/rocky-hq/contracts/go/agent"
	contractshearth "github.com/rocky-hq/contracts/go/hearth"

	"github.com/rocky-hq/hearth/internal/agent"
	"github.com/rocky-hq/hearth/internal/driver"
)

type Server struct {
	drv  driver.Driver
	emit agent.Emitter
}

func New(d driver.Driver) *Server { return &Server{drv: d, emit: agent.NopEmitter{}} }

// WithEmitter returns s with its Emitter replaced. Nil is treated as
// NopEmitter — nothing downstream should have to nil-check.
func (s *Server) WithEmitter(e agent.Emitter) *Server {
	if e == nil {
		s.emit = agent.NopEmitter{}
	} else {
		s.emit = e
	}
	return s
}

type ProvisionReq struct {
	Slug    string                     `json:"slug"`
	Profile driver.ProvisioningProfile `json:"profile"`
}
type StatusReq struct {
	Ref driver.DeploymentRef `json:"ref"`
}
type StatusResp struct {
	Status driver.Status `json:"status"`
}
type UpgradeReq struct {
	Ref     driver.DeploymentRef       `json:"ref"`
	Profile driver.ProvisioningProfile `json:"profile"`
}
type TeardownReq struct {
	Ref driver.DeploymentRef `json:"ref"`
}
type TeardownResp struct {
	OK bool `json:"ok"`
}
type ErrorBody struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}
type ErrorResp struct {
	Error ErrorBody `json:"error"`
}

func (s *Server) Mux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/provision", s.handleProvision)
	mux.HandleFunc("POST /v1/status", s.handleStatus)
	mux.HandleFunc("POST /v1/upgrade", s.handleUpgrade)
	mux.HandleFunc("POST /v1/teardown", s.handleTeardown)
	mux.HandleFunc("GET /v1/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]bool{"ok": true})
	})
	return mux
}

// agentInvocation carries the resolved identity + start-time for one
// RPC handler's lifecycle triple. Nil when the request lacks a
// workspace slug (no owner → no agent identity → no emission).
type agentInvocation struct {
	ctx          context.Context
	slug         string
	driverName   contractshearth.DriverName
	agentID      string
	invocationID string
	verb         agent.Verb
	startAt      time.Time
}

// beginInvocation fires agent.registered + agent.invoked when slug is
// non-empty. Returns nil (no-op completion) when slug is empty.
func (s *Server) beginInvocation(ctx context.Context, verb agent.Verb, slug string, driverName contractshearth.DriverName, requestSummary string) *agentInvocation {
	if slug == "" {
		return nil
	}
	inv := &agentInvocation{
		ctx:          ctx,
		slug:         slug,
		driverName:   driverName,
		agentID:      agent.BuildAgentID(slug, driverName),
		invocationID: uuid.NewString(),
		verb:         verb,
		startAt:      time.Now(),
	}
	nowUTC := inv.startAt.UTC()
	reg := agent.BuildRegistration(slug, driverName, nowUTC)
	s.emit.Emit(ctx, contractsagent.AgentHatchEvent{
		Kind:              contractsagent.AgentRegistered,
		Ts:                nowUTC,
		Actor:             "",
		AgentID:           inv.agentID,
		AgentRegistration: &reg,
	})
	capability := agent.CapabilityForVerb(verb)
	summary := truncate(requestSummary, 280)
	s.emit.Emit(ctx, contractsagent.AgentHatchEvent{
		Kind:           contractsagent.AgentInvoked,
		Ts:             nowUTC,
		Actor:          "",
		AgentID:        inv.agentID,
		InvocationID:   &inv.invocationID,
		Capability:     &capability,
		RequestSummary: &summary,
	})
	return inv
}

// finishInvocation fires agent.completed with the resolved outcome. It
// is a no-op when inv is nil (slug was empty at begin time).
func (s *Server) finishInvocation(inv *agentInvocation, err error) {
	if inv == nil {
		return
	}
	outcome := contractsagent.Ok
	if err != nil {
		outcome = contractsagent.Error
	}
	dur := time.Since(inv.startAt).Milliseconds()
	s.emit.Emit(inv.ctx, contractsagent.AgentHatchEvent{
		Kind:         contractsagent.AgentCompleted,
		Ts:           time.Now().UTC(),
		Actor:        "",
		AgentID:      inv.agentID,
		InvocationID: &inv.invocationID,
		Outcome:      &outcome,
		DurationMS:   &dur,
	})
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}

func (s *Server) handleProvision(w http.ResponseWriter, r *http.Request) {
	var req ProvisionReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, "invalid_request", err.Error(), false)
		return
	}
	inv := s.beginInvocation(r.Context(), agent.VerbProvision, req.Slug, req.Profile.Driver, req.Slug)
	ref, err := s.drv.Provision(r.Context(), req.Slug, req.Profile)
	s.finishInvocation(inv, err)
	if err != nil {
		writeDriverError(w, err)
		return
	}
	writeJSON(w, 200, ref)
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	var req StatusReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, "invalid_request", err.Error(), false)
		return
	}
	inv := s.beginInvocation(r.Context(), agent.VerbStatus, req.Ref.WorkspaceSlug, req.Ref.Driver, req.Ref.WorkspaceSlug)
	st, err := s.drv.Status(r.Context(), req.Ref)
	s.finishInvocation(inv, err)
	if err != nil {
		writeDriverError(w, err)
		return
	}
	writeJSON(w, 200, StatusResp{Status: st})
}

func (s *Server) handleUpgrade(w http.ResponseWriter, r *http.Request) {
	var req UpgradeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, "invalid_request", err.Error(), false)
		return
	}
	inv := s.beginInvocation(r.Context(), agent.VerbUpgrade, req.Ref.WorkspaceSlug, req.Ref.Driver, req.Ref.WorkspaceSlug)
	ref, err := s.drv.Upgrade(r.Context(), req.Ref, req.Profile)
	s.finishInvocation(inv, err)
	if err != nil {
		writeDriverError(w, err)
		return
	}
	writeJSON(w, 200, ref)
}

func (s *Server) handleTeardown(w http.ResponseWriter, r *http.Request) {
	var req TeardownReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, "invalid_request", err.Error(), false)
		return
	}
	inv := s.beginInvocation(r.Context(), agent.VerbTeardown, req.Ref.WorkspaceSlug, req.Ref.Driver, req.Ref.WorkspaceSlug)
	err := s.drv.Teardown(r.Context(), req.Ref)
	s.finishInvocation(inv, err)
	if err != nil {
		writeDriverError(w, err)
		return
	}
	writeJSON(w, 200, TeardownResp{OK: true})
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		_ = err // header + status already flushed; nothing actionable left
	}
}

func writeError(w http.ResponseWriter, code int, errCode, msg string, retryable bool) {
	writeJSON(w, code, ErrorResp{Error: ErrorBody{Code: errCode, Message: msg, Retryable: retryable}})
}

func writeDriverError(w http.ResponseWriter, err error) {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		writeError(w, 499, "context_canceled", err.Error(), true)
		return
	}
	writeError(w, 500, "driver_failure", err.Error(), false)
}

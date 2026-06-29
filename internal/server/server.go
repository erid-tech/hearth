// Package server implements the JSON-over-HTTP RPC layer in front of
// any driver.Driver. Phase 5c bind is a Unix socket; Phase 6 adds TCP
// + bearer auth.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/rocky-hq/hearth/internal/driver"
)

type Server struct {
	drv driver.Driver
}

func New(d driver.Driver) *Server { return &Server{drv: d} }

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

func (s *Server) handleProvision(w http.ResponseWriter, r *http.Request) {
	var req ProvisionReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, "invalid_request", err.Error(), false)
		return
	}
	ref, err := s.drv.Provision(r.Context(), req.Slug, req.Profile)
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
	st, err := s.drv.Status(r.Context(), req.Ref)
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
	ref, err := s.drv.Upgrade(r.Context(), req.Ref, req.Profile)
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
	if err := s.drv.Teardown(r.Context(), req.Ref); err != nil {
		writeDriverError(w, err)
		return
	}
	writeJSON(w, 200, TeardownResp{OK: true})
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
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

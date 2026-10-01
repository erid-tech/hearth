package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/erid-tech/hearth/internal/driver"
	"github.com/erid-tech/hearth/internal/driver/fake"
)

func newTestServer(t *testing.T) (*httptest.Server, *fake.FakeDriver) {
	t.Helper()
	d := fake.New()
	srv := httptest.NewServer(New(d).Mux())
	t.Cleanup(srv.Close)
	return srv, d
}

func soloProfile() driver.ProvisioningProfile {
	return driver.ProvisioningProfile{
		Tier:   driver.TierSolo,
		Driver: driver.DriverLocalDocker,
		ResourceCaps: driver.ResourceCaps{
			Seats: 1, CairnetStorageMB: 256, LoreRetentionDays: 7, VectorIndex: "hnsw",
		},
		DriverFlags: map[string]any{},
	}
}

// httpGet issues a context-bearing GET against url; satisfies the noctx lint.
func httpGet(t *testing.T, url string) (*http.Response, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	return http.DefaultClient.Do(req)
}

// httpPost issues a context-bearing POST against url; satisfies the noctx lint.
func httpPost(t *testing.T, url, contentType string, body io.Reader) (*http.Response, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	return http.DefaultClient.Do(req)
}

func TestHealthz(t *testing.T) {
	srv, _ := newTestServer(t)
	resp, err := httpGet(t, srv.URL+"/v1/healthz")
	if err != nil {
		t.Fatalf("healthz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d", resp.StatusCode)
	}
}

func TestProvisionHappyPath(t *testing.T) {
	srv, _ := newTestServer(t)
	body, _ := json.Marshal(ProvisionReq{Slug: "alex-solo", Profile: soloProfile()})
	resp, err := httpPost(t, srv.URL+"/v1/provision", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var ref driver.DeploymentRef
	if err := json.NewDecoder(resp.Body).Decode(&ref); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if ref.WorkspaceSlug != "alex-solo" {
		t.Errorf("ref.WorkspaceSlug = %q", ref.WorkspaceSlug)
	}
}

func TestProvisionMalformedJSON(t *testing.T) {
	srv, _ := newTestServer(t)
	resp, err := httpPost(t, srv.URL+"/v1/provision", "application/json", strings.NewReader("{not json"))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	var body ErrorResp
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Error.Code != "invalid_request" {
		t.Errorf("code = %q", body.Error.Code)
	}
}

func TestProvisionContextCanceled(t *testing.T) {
	srv, _ := newTestServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	body, _ := json.Marshal(ProvisionReq{Slug: "alex-solo", Profile: soloProfile()})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/v1/provision", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err == nil {
		resp.Body.Close()
		t.Fatal("expected error from canceled context")
	}
}

func TestUnknownRoute404(t *testing.T) {
	srv, _ := newTestServer(t)
	resp, err := httpGet(t, srv.URL+"/v1/nope")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Errorf("status = %d", resp.StatusCode)
	}
}

func TestStatusRoundTrip(t *testing.T) {
	srv, _ := newTestServer(t)
	body, _ := json.Marshal(ProvisionReq{Slug: "alex-solo", Profile: soloProfile()})
	pResp, err := httpPost(t, srv.URL+"/v1/provision", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	defer pResp.Body.Close()
	var ref driver.DeploymentRef
	_ = json.NewDecoder(pResp.Body).Decode(&ref)

	sBody, _ := json.Marshal(StatusReq{Ref: ref})
	sResp, err := httpPost(t, srv.URL+"/v1/status", "application/json", bytes.NewReader(sBody))
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	defer sResp.Body.Close()
	var st StatusResp
	if err := json.NewDecoder(sResp.Body).Decode(&st); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if st.Status == "" {
		t.Errorf("Status empty")
	}
}

func TestUpgradeHappyPath(t *testing.T) {
	srv, _ := newTestServer(t)
	body, _ := json.Marshal(ProvisionReq{Slug: "alex-solo", Profile: soloProfile()})
	pResp, err := httpPost(t, srv.URL+"/v1/provision", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	defer pResp.Body.Close()
	var ref driver.DeploymentRef
	_ = json.NewDecoder(pResp.Body).Decode(&ref)

	uBody, _ := json.Marshal(UpgradeReq{Ref: ref, Profile: soloProfile()})
	uResp, err := httpPost(t, srv.URL+"/v1/upgrade", "application/json", bytes.NewReader(uBody))
	if err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	defer uResp.Body.Close()
	if uResp.StatusCode != 200 {
		t.Fatalf("status = %d", uResp.StatusCode)
	}
	var out driver.DeploymentRef
	if err := json.NewDecoder(uResp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.WorkspaceSlug != "alex-solo" {
		t.Errorf("out.WorkspaceSlug = %q", out.WorkspaceSlug)
	}
}

func TestTeardownHappyPath(t *testing.T) {
	srv, _ := newTestServer(t)
	body, _ := json.Marshal(ProvisionReq{Slug: "alex-solo", Profile: soloProfile()})
	pResp, err := httpPost(t, srv.URL+"/v1/provision", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	defer pResp.Body.Close()
	var ref driver.DeploymentRef
	_ = json.NewDecoder(pResp.Body).Decode(&ref)

	tBody, _ := json.Marshal(TeardownReq{Ref: ref})
	tResp, err := httpPost(t, srv.URL+"/v1/teardown", "application/json", bytes.NewReader(tBody))
	if err != nil {
		t.Fatalf("teardown: %v", err)
	}
	defer tResp.Body.Close()
	if tResp.StatusCode != 200 {
		t.Fatalf("status = %d", tResp.StatusCode)
	}
	var out TeardownResp
	if err := json.NewDecoder(tResp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !out.OK {
		t.Errorf("ok = false")
	}
}

// errDriver is a driver.Driver stub whose Status always errors — used
// to exercise the 500/driver_failure branch (fake.Driver never errors
// outside of context cancellation).
type errDriver struct{ *fake.FakeDriver }

func (errDriver) Status(_ context.Context, _ driver.DeploymentRef) (driver.Status, error) {
	return "", errors.New("boom")
}

func TestDriverFailure500(t *testing.T) {
	d := errDriver{FakeDriver: fake.New()}
	srv := httptest.NewServer(New(d).Mux())
	t.Cleanup(srv.Close)

	body, _ := json.Marshal(StatusReq{Ref: driver.DeploymentRef{
		WorkspaceSlug: "ghost", Tier: driver.TierSolo, Driver: driver.DriverLocalDocker,
	}})
	resp, err := httpPost(t, srv.URL+"/v1/status", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 500 {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
	var er ErrorResp
	if err := json.NewDecoder(resp.Body).Decode(&er); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if er.Error.Code != "driver_failure" {
		t.Errorf("code = %q", er.Error.Code)
	}
	if er.Error.Retryable {
		t.Errorf("retryable = true, want false")
	}
}

func TestUnknownFieldsPassThrough(t *testing.T) {
	srv, _ := newTestServer(t)
	body := []byte(`{"slug":"alex-solo","profile":{"tier":"solo","driver":"local-docker","resource_caps":{"seats":1,"cairnet_storage_mb":256,"lore_retention_days":7,"vector_index":"hnsw"},"driver_flags":{},"future_field":"ignored"}}`)
	resp, err := httpPost(t, srv.URL+"/v1/provision", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("KILN-extensibility: extra field rejected (status=%d)", resp.StatusCode)
	}
}

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/erid-tech/hearth/internal/driver"
	"github.com/erid-tech/hearth/internal/server"
)

// TestRPCEndToEnd builds cmd/hearth, launches it pointed at a temp
// Unix socket configured for the local-docker driver, and drives all
// four verbs over JSON-over-HTTP — proving wire compatibility with the
// `@rocky-hq/contracts/go/hearth` shapes consumed by the binary.
func TestRPCEndToEnd(t *testing.T) {
	requireIntegration(t)
	cli := newClient(t)
	s := slug(t)
	t.Cleanup(func() { cleanup(t, cli, s) })

	dir := t.TempDir()
	bin := filepath.Join(dir, "hearth")
	build := exec.Command("go", "build", "-o", bin, "../../cmd/hearth")
	build.Stdout = os.Stdout
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("go build ./cmd/hearth: %v", err)
	}

	sock := filepath.Join(dir, "h.sock")
	cmd := exec.Command(bin)
	cmd.Env = append(
		os.Environ(),
		"ROCKY_HEARTH_DRIVER=local-docker",
		"ROCKY_HEARTH_SOCKET="+sock,
		"ROCKY_HEARTH_IMAGE_CAIRNET=nginx:alpine",
		"ROCKY_HEARTH_IMAGE_LORE=nginx:alpine",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start hearth: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Signal(os.Interrupt)
		done := make(chan error, 1)
		go func() { _, err := cmd.Process.Wait(); done <- err }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	})

	// Wait for the socket to appear.
	deadline := time.Now().Add(10 * time.Second)
	for {
		if c, err := net.Dial("unix", sock); err == nil {
			_ = c.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("socket never appeared at " + sock)
		}
		time.Sleep(50 * time.Millisecond)
	}

	client := &http.Client{
		Timeout: 120 * time.Second,
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", sock)
			},
		},
	}

	post := func(t *testing.T, path string, body any, out any) {
		t.Helper()
		buf, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal %s: %v", path, err)
		}
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://unix"+path, bytes.NewReader(buf))
		if err != nil {
			t.Fatalf("new request %s: %v", path, err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("POST %s: %v", path, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("POST %s: status=%d", path, resp.StatusCode)
		}
		if out != nil {
			if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
				t.Fatalf("decode %s: %v", path, err)
			}
		}
	}

	// Provision.
	var ref driver.DeploymentRef
	post(t, "/v1/provision", server.ProvisionReq{Slug: s, Profile: soloProfile()}, &ref)
	if ref.WorkspaceSlug != s {
		t.Errorf("provision ref.WorkspaceSlug = %q, want %q", ref.WorkspaceSlug, s)
	}
	if ref.LastStatus != driver.StatusReady {
		t.Errorf("provision ref.LastStatus = %q, want %q", ref.LastStatus, driver.StatusReady)
	}

	// Status.
	var stResp server.StatusResp
	post(t, "/v1/status", server.StatusReq{Ref: ref}, &stResp)
	if stResp.Status != driver.StatusReady {
		t.Errorf("status = %q, want %q", stResp.Status, driver.StatusReady)
	}

	// Upgrade.
	var upRef driver.DeploymentRef
	post(t, "/v1/upgrade", server.UpgradeReq{Ref: ref, Profile: soloProfile()}, &upRef)
	if upRef.WorkspaceSlug != ref.WorkspaceSlug {
		t.Errorf("upgrade slug drift: got %q want %q", upRef.WorkspaceSlug, ref.WorkspaceSlug)
	}

	// Teardown.
	var tdResp server.TeardownResp
	post(t, "/v1/teardown", server.TeardownReq{Ref: ref}, &tdResp)
}

package server

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/erid-tech/hearth/internal/driver"
)

func TestWireRoundTrip(t *testing.T) {
	cases := []any{
		ProvisionReq{Slug: "alex-solo", Profile: driver.ProvisioningProfile{
			Tier: driver.TierSolo, Driver: driver.DriverLocalDocker,
			ResourceCaps: driver.ResourceCaps{Seats: 1, CairnetStorageMB: 256, LoreRetentionDays: 7, VectorIndex: "hnsw"},
			DriverFlags:  map[string]any{},
		}},
		StatusReq{Ref: driver.DeploymentRef{
			WorkspaceSlug: "alex-solo", Tier: driver.TierSolo, Driver: driver.DriverLocalDocker,
			Endpoint: "unix:///x.sock", LastStatus: driver.StatusReady, Created: time.Unix(0, 0).UTC(),
		}},
		StatusResp{Status: driver.StatusReady},
		TeardownResp{OK: true},
		ErrorResp{Error: ErrorBody{Code: "invalid_request", Message: "bad json", Retryable: false}},
	}
	for _, in := range cases {
		raw, err := json.Marshal(in)
		if err != nil {
			t.Fatalf("marshal %T: %v", in, err)
		}
		out := reflect.New(reflect.TypeOf(in)).Interface()
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatalf("unmarshal %T: %v", in, err)
		}
		got := reflect.ValueOf(out).Elem().Interface()
		if !reflect.DeepEqual(in, got) {
			t.Errorf("%T round-trip mismatch:\n in: %#v\nout: %#v", in, in, got)
		}
	}
}

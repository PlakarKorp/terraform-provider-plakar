// Copyright (c) 2026 PlakarKorp
// ISC License (see LICENSE)

package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"

	"github.com/PlakarKorp/terraform-provider-plakar/internal/client"
)

// A destroy uninstalls the version in state and nothing else: an earlier
// version left installed by a version change stays.
func TestIntegrationDeleteUninstallsTheVersionInState(t *testing.T) {
	var mu sync.Mutex
	var requests []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/auth/login/apikey" {
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "t"})
			return
		}
		mu.Lock()
		requests = append(requests, r.Method+" "+r.URL.Path)
		mu.Unlock()
	}))
	t.Cleanup(srv.Close)
	r := &integrationResource{client: client.New(srv.URL, "key", "")}
	state, _ := integrationValue(t, r, model("id-v1.1.0", "v1.1.0"))
	resp := resource.DeleteResponse{State: state}
	r.Delete(context.Background(), resource.DeleteRequest{State: state}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("delete: %v", resp.Diagnostics)
	}
	if len(requests) != 1 || requests[0] != "DELETE /api/v1/integrations/installed/id-v1.1.0" {
		t.Errorf("requests %v, want the uninstall of the version in state only", requests)
	}
}

// The listing's name filter is not taken on trust: when it answers with
// other integrations only, none of that name is current, so the resource
// leaves state and the plan installs it again.
func TestIntegrationReadIgnoresOtherNames(t *testing.T) {
	ctx := context.Background()
	r := newFakeIntegrations(t, &fakeIntegrations{installed: []client.InstalledIntegration{
		{ID: "id-other", Name: "other", Version: "v1.0.0", IsCurrent: true}}})
	state, _ := integrationValue(t, r, model("id-v1.0.0", "v1.0.0"))
	resp := resource.ReadResponse{State: state}
	r.Read(ctx, resource.ReadRequest{State: state}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("read: %v", resp.Diagnostics)
	}
	if !resp.State.Raw.IsNull() {
		t.Error("a listing holding another integration only kept the resource in state")
	}
}

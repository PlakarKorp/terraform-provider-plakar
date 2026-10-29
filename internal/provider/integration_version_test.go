// Copyright (c) 2026 PlakarKorp
// ISC License (see LICENSE)

package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/PlakarKorp/terraform-provider-plakar/internal/client"
)

// Version changes cannot be exercised against a live catalog that offers one
// version per integration, so these tests run the resource's own Update and
// Read, and the data source's lookup, against a fake Control Plane.

// fakeIntegrations serves the routes the integration code uses and records
// every install request.
type fakeIntegrations struct {
	mu        sync.Mutex
	catalog   []string                      // versions of "tool"
	installed []client.InstalledIntegration // rows of the installed listing
	installs  []map[string]string           // bodies of install requests
}

func (f *fakeIntegrations) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	page := func(items any, total int) {
		_ = json.NewEncoder(w).Encode(map[string]any{"items": items, "total": total})
	}
	switch {
	case r.URL.Path == "/api/v1/auth/login/apikey":
		_ = json.NewEncoder(w).Encode(map[string]string{"token": "t"})
	case r.URL.Path == "/api/v1/integrations/catalog/tool/versions":
		var items []map[string]string
		for _, v := range f.catalog {
			items = append(items, map[string]string{"name": "tool", "version": v})
		}
		page(items, len(items))
	case r.URL.Path == "/api/v1/integrations/installed" && r.Method == "GET":
		page(f.installed, len(f.installed))
	case r.URL.Path == "/api/v1/integrations/installed/current" && r.Method == "POST":
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.installs = append(f.installs, body)
		id := "id-" + body["version"]
		for i := range f.installed {
			f.installed[i].IsCurrent = false
		}
		f.installed = append(f.installed, client.InstalledIntegration{
			ID: id, Name: body["name"], Version: body["version"], IsCurrent: true})
		_ = json.NewEncoder(w).Encode(map[string]string{"id": id})
	default:
		http.NotFound(w, r)
	}
}

func newFakeIntegrations(t *testing.T, f *fakeIntegrations) *integrationResource {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return &integrationResource{client: client.New(srv.URL, "key", "")}
}

// integrationValue builds the raw value of the resource's schema for a model.
func integrationValue(t *testing.T, r *integrationResource, m *integrationModel) (tfsdk.State, tfsdk.Plan) {
	t.Helper()
	ctx := context.Background()
	var sresp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &sresp)
	empty := tftypes.NewValue(sresp.Schema.Type().TerraformType(ctx), nil)
	state := tfsdk.State{Schema: sresp.Schema, Raw: empty}
	if m != nil {
		if diags := state.Set(ctx, m); diags.HasError() {
			t.Fatalf("building state: %v", diags)
		}
	}
	return state, tfsdk.Plan{Schema: sresp.Schema, Raw: state.Raw}
}

func model(id, version string) *integrationModel {
	m := &integrationModel{Name: types.StringValue("tool"), ID: types.StringUnknown(), Version: types.StringValue(version)}
	if id != "" {
		m.ID = types.StringValue(id)
	}
	return m
}

func runUpdate(t *testing.T, r *integrationResource, prior, planned *integrationModel) integrationModel {
	t.Helper()
	ctx := context.Background()
	state, _ := integrationValue(t, r, prior)
	_, plan := integrationValue(t, r, planned)
	resp := resource.UpdateResponse{State: state}
	r.Update(ctx, resource.UpdateRequest{State: state, Plan: plan}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("update: %v", resp.Diagnostics)
	}
	var got integrationModel
	resp.State.Get(ctx, &got)
	return got
}

// CS-M2-14 and 15: a new version, newer or older, is one request that
// installs it and makes it current, carrying the name and that version.
func TestIntegrationUpdateInstallsTheNewVersion(t *testing.T) {
	for _, tc := range []struct{ from, to string }{
		{"v1.0.0", "v1.1.0"}, // upgrade
		{"v1.1.0", "v1.0.0"}, // downgrade takes the same path
	} {
		f := &fakeIntegrations{
			catalog:   []string{"v1.0.0", "v1.1.0"},
			installed: []client.InstalledIntegration{{ID: "id-" + tc.from, Name: "tool", Version: tc.from, IsCurrent: true}},
		}
		r := newFakeIntegrations(t, f)
		got := runUpdate(t, r, model("id-"+tc.from, tc.from), model("", tc.to))
		if len(f.installs) != 1 || f.installs[0]["name"] != "tool" || f.installs[0]["version"] != tc.to {
			t.Errorf("%s -> %s: install requests %v, want one for tool %s", tc.from, tc.to, f.installs, tc.to)
		}
		if got.ID.ValueString() != "id-"+tc.to || got.Version.ValueString() != tc.to {
			t.Errorf("%s -> %s: state holds %s at %s", tc.from, tc.to, got.ID, got.Version)
		}
	}
}

// CS-M2-14: the same version sends nothing.
func TestIntegrationUpdateSameVersionSendsNothing(t *testing.T) {
	f := &fakeIntegrations{
		catalog:   []string{"v1.0.0"},
		installed: []client.InstalledIntegration{{ID: "id-v1.0.0", Name: "tool", Version: "v1.0.0", IsCurrent: true}},
	}
	r := newFakeIntegrations(t, f)
	got := runUpdate(t, r, model("id-v1.0.0", "v1.0.0"), model("id-v1.0.0", "v1.0.0"))
	if len(f.installs) != 0 {
		t.Errorf("install requests %v, want none", f.installs)
	}
	if got.ID.ValueString() != "id-v1.0.0" {
		t.Errorf("state holds %s", got.ID)
	}
}

// CS-M2-15: a version the catalog does not offer is an error naming it, and
// nothing is installed.
func TestIntegrationUpdateUnknownVersion(t *testing.T) {
	f := &fakeIntegrations{
		catalog:   []string{"v1.1.0"},
		installed: []client.InstalledIntegration{{ID: "id-v1.1.0", Name: "tool", Version: "v1.1.0", IsCurrent: true}},
	}
	r := newFakeIntegrations(t, f)
	ctx := context.Background()
	state, _ := integrationValue(t, r, model("id-v1.1.0", "v1.1.0"))
	_, plan := integrationValue(t, r, model("", "v0.9.0"))
	resp := resource.UpdateResponse{State: state}
	r.Update(ctx, resource.UpdateRequest{State: state, Plan: plan}, &resp)
	if !resp.Diagnostics.HasError() || !strings.Contains(resp.Diagnostics.Errors()[0].Detail(), `"v0.9.0"`) {
		t.Errorf("diagnostics %v, want an error naming v0.9.0", resp.Diagnostics)
	}
	if len(f.installs) != 0 {
		t.Errorf("install requests %v, want none", f.installs)
	}
}

// CS-M2-18: a Read that finds another version current writes it to state, so
// a pinned version plans the update back; with none current the resource
// leaves state and the plan installs it again.
func TestIntegrationReadFollowsTheCurrentVersion(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name      string
		installed []client.InstalledIntegration
		wantID    string // empty: removed from state
	}{
		{"still current", []client.InstalledIntegration{
			{ID: "id-v1.0.0", Name: "tool", Version: "v1.0.0", IsCurrent: true}}, "id-v1.0.0"},
		{"another made current", []client.InstalledIntegration{
			{ID: "id-v1.0.0", Name: "tool", Version: "v1.0.0"},
			{ID: "id-v1.1.0", Name: "tool", Version: "v1.1.0", IsCurrent: true}}, "id-v1.1.0"},
		{"none current", []client.InstalledIntegration{
			{ID: "id-v1.0.0", Name: "tool", Version: "v1.0.0"}}, ""},
		{"uninstalled", nil, ""},
	} {
		r := newFakeIntegrations(t, &fakeIntegrations{installed: tc.installed})
		state, _ := integrationValue(t, r, model("id-v1.0.0", "v1.0.0"))
		resp := resource.ReadResponse{State: state}
		r.Read(ctx, resource.ReadRequest{State: state}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("%s: %v", tc.name, resp.Diagnostics)
		}
		if tc.wantID == "" {
			if !resp.State.Raw.IsNull() {
				t.Errorf("%s: still in state", tc.name)
			}
			continue
		}
		var got integrationModel
		resp.State.Get(ctx, &got)
		if got.ID.ValueString() != tc.wantID || got.Version.ValueString() != strings.TrimPrefix(tc.wantID, "id-") {
			t.Errorf("%s: state holds %s at %s, want %s", tc.name, got.ID, got.Version, tc.wantID)
		}
	}
}

// CS-M2-03b: the data source's lookup picks the current version among
// several, takes a single version even when not current, and refuses
// several with none current.
func TestIntegrationByNamePicksTheCurrentVersion(t *testing.T) {
	for _, tc := range []struct {
		name      string
		installed []client.InstalledIntegration
		wantID    string // empty: an error
	}{
		{"one current of two", []client.InstalledIntegration{
			{ID: "a", Name: "tool", Version: "v1.0.0"},
			{ID: "b", Name: "tool", Version: "v1.1.0", IsCurrent: true}}, "b"},
		{"single, not current", []client.InstalledIntegration{
			{ID: "a", Name: "tool", Version: "v1.0.0"}}, "a"},
		{"two, none current", []client.InstalledIntegration{
			{ID: "a", Name: "tool", Version: "v1.0.0"},
			{ID: "b", Name: "tool", Version: "v1.1.0"}}, ""},
	} {
		r := newFakeIntegrations(t, &fakeIntegrations{installed: tc.installed})
		got, err := r.client.IntegrationByName("tool")
		switch {
		case tc.wantID == "" && err == nil:
			t.Errorf("%s: got %s, want an error", tc.name, got.ID)
		case tc.wantID != "" && (err != nil || got.ID != tc.wantID):
			t.Errorf("%s: got %v, %v; want %s", tc.name, got, err, tc.wantID)
		}
	}
}

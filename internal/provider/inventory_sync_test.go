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

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/PlakarKorp/terraform-provider-plakar/internal/client"
)

// When the sync runs is counted in requests, which a live Control Plane does
// not report, so these tests run the resource's own Create, Update and Read
// against a fake one.

// fakeInventory serves one aws inventory and records every request but the
// login.
type fakeInventory struct {
	mu       sync.Mutex
	inv      client.Inventory
	synced   *string // last_update in the listing
	syncFail string  // error of the next syncs, none when empty
	postFail bool    // the configuration update answers 500
	requests []string
}

func (f *fakeInventory) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.URL.Path == "/api/v1/auth/login/apikey" {
		_ = json.NewEncoder(w).Encode(map[string]string{"token": "t"})
		return
	}
	f.requests = append(f.requests, r.Method+" "+r.URL.Path)
	write := func(v any) { _ = json.NewEncoder(w).Encode(v) }
	setConfig := func() {
		var body client.InventoryRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.inv.Name, f.inv.Type, f.inv.AWS = body.Name, body.Type, body.AWS
		f.synced = nil // a configuration change clears the last sync
	}
	switch {
	case r.URL.Path == "/api/v1/account/me":
		write(map[string]string{"organization_id": "org"})
	case r.URL.Path == "/api/v1/account/organizations/org/inventories" && r.Method == "POST":
		setConfig()
		f.inv.ID = "inv"
		write(f.inv)
	case r.URL.Path == "/api/v1/account/organizations/org/inventories":
		write(map[string]any{"total": 1, "items": []client.InventorySummary{
			{ID: f.inv.ID, Name: f.inv.Name, Type: f.inv.Type, LastUpdate: f.synced}}})
	case r.URL.Path == "/api/v1/inventories/inv" && r.Method == "GET":
		write(f.inv)
	case r.URL.Path == "/api/v1/inventories/inv" && r.Method == "POST":
		if f.postFail {
			w.WriteHeader(http.StatusInternalServerError)
			write(map[string]string{"detail": "internal error"})
			return
		}
		setConfig()
	case r.URL.Path == "/api/v1/inventories/inv/sync":
		if f.syncFail != "" {
			write(map[string]any{"ok": false, "error": f.syncFail})
			return
		}
		at := "2026-10-30T10:51:43.589062Z"
		f.synced = &at
		write(map[string]any{"ok": true})
	default:
		http.NotFound(w, r)
	}
}

// count tells how many recorded requests start with prefix.
func (f *fakeInventory) count(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.requests {
		if strings.HasPrefix(r, prefix) {
			n++
		}
	}
	return n
}

const (
	postConfig = "POST /api/v1/" // create or update of the configuration
	getSync    = "GET /api/v1/inventories/inv/sync"
)

func newFakeInventory(t *testing.T, f *fakeInventory) *inventoryResource {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return &inventoryResource{client: client.New(srv.URL, "key", "")}
}

// awsInventory is an aws inventory model; triggers are key=value pairs.
func awsInventory(name string, sync bool, triggers ...string) *inventoryModel {
	m := &inventoryModel{
		ID: types.StringValue("inv"), Name: types.StringValue(name), Type: types.StringValue("aws"),
		Sync: types.BoolValue(sync), SyncTriggers: types.MapNull(types.StringType),
		LastSyncedAt: types.StringUnknown(),
		AWS: &inventoryAWSModel{CredentialsType: types.StringValue("iam"), Region: types.StringValue("eu-west-3"),
			AccessKey: types.StringNull(), SecretAccessKey: types.StringNull()},
	}
	if len(triggers) > 0 {
		elems := map[string]attr.Value{}
		for _, kv := range triggers {
			k, v, _ := strings.Cut(kv, "=")
			elems[k] = types.StringValue(v)
		}
		m.SyncTriggers = types.MapValueMust(types.StringType, elems)
	}
	return m
}

// inventoryRaw builds the raw value of the resource's schema for a model.
func inventoryRaw(t *testing.T, r *inventoryResource, m *inventoryModel) tfsdk.State {
	t.Helper()
	ctx := context.Background()
	var sresp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &sresp)
	state := tfsdk.State{Schema: sresp.Schema, Raw: tftypes.NewValue(sresp.Schema.Type().TerraformType(ctx), nil)}
	if m != nil {
		if diags := state.Set(ctx, m); diags.HasError() {
			t.Fatalf("building value: %v", diags)
		}
	}
	return state
}

func runCreate(t *testing.T, r *inventoryResource, planned *inventoryModel) (inventoryModel, resource.CreateResponse) {
	t.Helper()
	ctx := context.Background()
	planned.ID = types.StringUnknown()
	raw := inventoryRaw(t, r, planned)
	resp := resource.CreateResponse{State: inventoryRaw(t, r, nil)}
	r.Create(ctx, resource.CreateRequest{
		Plan:   tfsdk.Plan{Schema: raw.Schema, Raw: raw.Raw},
		Config: tfsdk.Config{Schema: raw.Schema, Raw: raw.Raw},
	}, &resp)
	var got inventoryModel
	resp.State.Get(ctx, &got)
	return got, resp
}

func runInventoryUpdate(t *testing.T, r *inventoryResource, prior, planned *inventoryModel) (inventoryModel, resource.UpdateResponse) {
	t.Helper()
	ctx := context.Background()
	state := inventoryRaw(t, r, prior)
	plan := inventoryRaw(t, r, planned)
	// The framework starts the response from the prior state.
	resp := resource.UpdateResponse{State: state}
	r.Update(ctx, resource.UpdateRequest{
		State:  state,
		Plan:   tfsdk.Plan{Schema: plan.Schema, Raw: plan.Raw},
		Config: tfsdk.Config{Schema: plan.Schema, Raw: plan.Raw},
	}, &resp)
	var got inventoryModel
	resp.State.Get(ctx, &got)
	return got, resp
}

// applied is the state of an inventory as it stands after an apply.
func applied(m *inventoryModel, lastSync string) *inventoryModel {
	m.LastSyncedAt = types.StringNull()
	if lastSync != "" {
		m.LastSyncedAt = types.StringValue(lastSync)
	}
	return m
}

func TestInventoryCreateSyncsWhenAsked(t *testing.T) {
	for _, sync := range []bool{true, false} {
		f := &fakeInventory{}
		r := newFakeInventory(t, f)
		got, resp := runCreate(t, r, awsInventory("inv", sync))
		if resp.Diagnostics.HasError() {
			t.Fatalf("sync = %v: create: %v", sync, resp.Diagnostics)
		}
		want := 0
		if sync {
			want = 1
		}
		if n := f.count(postConfig); n != 1 {
			t.Errorf("sync = %v: %d configuration writes, want 1", sync, n)
		}
		if n := f.count(getSync); n != want {
			t.Errorf("sync = %v: %d syncs, want %d", sync, n, want)
		}
		if sync {
			for i, req := range f.requests {
				if strings.HasPrefix(req, postConfig) && (i+1 == len(f.requests) || f.requests[i+1] != getSync) {
					t.Errorf("the sync is not the request right after the create: %v", f.requests)
				}
			}
		}
		if got.LastSyncedAt.IsNull() == sync {
			t.Errorf("sync = %v: last_synced_at = %v", sync, got.LastSyncedAt)
		}
	}
}

// A failed sync fails the create with the server's reason, and the inventory,
// which exists, stays in state.
func TestInventoryCreateSyncFailureKeepsTheInventory(t *testing.T) {
	f := &fakeInventory{syncFail: "Failed to synchronize inventory: key not found in secret"}
	r := newFakeInventory(t, f)
	got, resp := runCreate(t, r, awsInventory("inv", true))
	if !resp.Diagnostics.HasError() {
		t.Fatal("a failed sync did not fail the create")
	}
	detail := resp.Diagnostics.Errors()[0].Detail()
	for _, want := range []string{"created but did not synchronize", f.syncFail} {
		if !strings.Contains(detail, want) {
			t.Errorf("error %q does not say %q", detail, want)
		}
	}
	if got.ID.ValueString() != "inv" || !got.LastSyncedAt.IsNull() {
		t.Errorf("state after the failure: id %v, last_synced_at %v", got.ID, got.LastSyncedAt)
	}
}

func TestInventoryUpdateSyncsOnlyWhenTheInventoryIsWritten(t *testing.T) {
	for _, tc := range []struct {
		name          string
		prior, plan   *inventoryModel
		posts, syncs  int
		noRequestsAll bool
	}{
		{name: "configuration change with sync",
			prior: awsInventory("a", true), plan: awsInventory("b", true), posts: 1, syncs: 1},
		{name: "configuration change without sync",
			prior: awsInventory("a", false), plan: awsInventory("b", false), posts: 1, syncs: 0},
		{name: "sync turns true",
			prior: awsInventory("a", false), plan: awsInventory("a", true), posts: 0, syncs: 1},
		{name: "sync turns false",
			prior: awsInventory("a", true), plan: awsInventory("a", false), noRequestsAll: true},
		{name: "triggers change with sync",
			prior: awsInventory("a", true, "retry=1"), plan: awsInventory("a", true, "retry=2"), posts: 0, syncs: 1},
		{name: "triggers set with sync",
			prior: awsInventory("a", true), plan: awsInventory("a", true, "retry=1"), posts: 0, syncs: 1},
		{name: "triggers change without sync",
			prior: awsInventory("a", false, "retry=1"), plan: awsInventory("a", false, "retry=2"), noRequestsAll: true},
	} {
		f := &fakeInventory{inv: client.Inventory{ID: "inv", Name: "a", Type: "aws"}}
		r := newFakeInventory(t, f)
		got, resp := runInventoryUpdate(t, r, applied(tc.prior, "2026-10-30T09:00:00Z"), tc.plan)
		if resp.Diagnostics.HasError() {
			t.Fatalf("%s: update: %v", tc.name, resp.Diagnostics)
		}
		if tc.noRequestsAll {
			if len(f.requests) != 0 {
				t.Errorf("%s: requests %v, want none", tc.name, f.requests)
			}
			if got.LastSyncedAt.ValueString() != "2026-10-30T09:00:00Z" {
				t.Errorf("%s: last_synced_at %v, want the state's", tc.name, got.LastSyncedAt)
			}
			// State takes the planned sync attributes, or every plan would
			// propose the same change again.
			if !got.Sync.Equal(tc.plan.Sync) || !got.SyncTriggers.Equal(tc.plan.SyncTriggers) {
				t.Errorf("%s: state sync %v, sync_triggers %v, want the plan's", tc.name, got.Sync, got.SyncTriggers)
			}
			continue
		}
		if n := f.count(postConfig); n != tc.posts {
			t.Errorf("%s: %d configuration writes, want %d (%v)", tc.name, n, tc.posts, f.requests)
		}
		if n := f.count(getSync); n != tc.syncs {
			t.Errorf("%s: %d syncs, want %d (%v)", tc.name, n, tc.syncs, f.requests)
		}
		if got.Name.ValueString() != tc.plan.Name.ValueString() || got.LastSyncedAt.IsUnknown() {
			t.Errorf("%s: state name %v, last_synced_at %v", tc.name, got.Name, got.LastSyncedAt)
		}
	}
}

// A failed sync at update fails the apply and leaves the new configuration in
// state, so the retry is a change to sync_triggers.
func TestInventoryUpdateSyncFailureKeepsTheNewConfiguration(t *testing.T) {
	f := &fakeInventory{inv: client.Inventory{ID: "inv", Name: "a", Type: "aws"},
		syncFail: "Failed to synchronize inventory: list buckets: Access Denied"}
	r := newFakeInventory(t, f)
	got, resp := runInventoryUpdate(t, r, applied(awsInventory("a", true), ""), awsInventory("b", true))
	if !resp.Diagnostics.HasError() {
		t.Fatal("a failed sync did not fail the update")
	}
	detail := resp.Diagnostics.Errors()[0].Detail()
	for _, want := range []string{"updated but did not synchronize", f.syncFail, "sync_triggers"} {
		if !strings.Contains(detail, want) {
			t.Errorf("error %q does not say %q", detail, want)
		}
	}
	if got.Name.ValueString() != "b" {
		t.Errorf("state name %v, want the new configuration", got.Name)
	}
}

func TestInventorySelfManagedRefusesSync(t *testing.T) {
	ctx := context.Background()
	r := &inventoryResource{}
	m := &inventoryModel{ID: types.StringNull(), Name: types.StringValue("hand"), Type: types.StringValue("self-managed"),
		Sync: types.BoolValue(true), SyncTriggers: types.MapNull(types.StringType), LastSyncedAt: types.StringNull()}
	raw := inventoryRaw(t, r, m)
	var resp resource.ValidateConfigResponse
	r.ValidateConfig(ctx, resource.ValidateConfigRequest{Config: tfsdk.Config{Schema: raw.Schema, Raw: raw.Raw}}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("sync = true on a self-managed inventory passed validation")
	}
	for _, d := range resp.Diagnostics.Errors() {
		if wp, ok := d.(interface{ Path() path.Path }); !ok || !wp.Path().Equal(path.Root("sync")) {
			t.Errorf("error not on sync: %v", d)
		}
	}
}

// A state from before sync existed holds it null; read, it becomes false, the
// default, and the plan stays empty.
func TestInventoryReadDefaultsSync(t *testing.T) {
	ctx := context.Background()
	f := &fakeInventory{inv: client.Inventory{ID: "inv", Name: "a", Type: "aws",
		AWS: &client.AWSInventoryConfig{CredentialsType: "iam", Region: "eu-west-3"}}}
	r := newFakeInventory(t, f)
	prior := applied(awsInventory("a", false), "")
	prior.Sync = types.BoolNull()
	state := inventoryRaw(t, r, prior)
	resp := resource.ReadResponse{State: state}
	r.Read(ctx, resource.ReadRequest{State: state}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("read: %v", resp.Diagnostics)
	}
	var got inventoryModel
	resp.State.Get(ctx, &got)
	if got.Sync.IsNull() || got.Sync.ValueBool() {
		t.Errorf("sync after read = %v, want false", got.Sync)
	}
	if n := f.count(getSync); n != 0 {
		t.Errorf("read asked for %d syncs", n)
	}
}

// An update the server refused leaves the prior state: a write-only rotation
// whose request failed keeps its old version in state, so the next apply
// sends it again.
func TestInventoryUpdateFailureKeepsThePriorState(t *testing.T) {
	ctx := context.Background()
	f := &fakeInventory{inv: client.Inventory{ID: "inv", Name: "scw", Type: "scaleway",
		Scaleway: &client.ScalewayInventoryConfig{AccessKey: client.Field{Value: "AK"}, SecretKey: client.Field{Value: "old"}}},
		postFail: true}
	r := newFakeInventory(t, f)
	scw := func(version int64) *inventoryModel {
		m := &inventoryModel{ID: types.StringValue("inv"), Name: types.StringValue("scw"), Type: types.StringValue("scaleway"),
			Sync: types.BoolValue(true), SyncTriggers: types.MapNull(types.StringType), LastSyncedAt: types.StringUnknown(),
			Scaleway: newInventoryScalewayModel()}
		m.Scaleway.ProjectID = types.StringValue("p")
		m.Scaleway.AccessKey = types.StringValue("AK")
		m.Scaleway.SecretKeyWOVersion = types.Int64Value(version)
		return m
	}
	prior := applied(scw(1), "")
	planned := scw(2)
	state := inventoryRaw(t, r, prior)
	plan := inventoryRaw(t, r, planned)
	cfg := *planned
	cfg.Scaleway = newInventoryScalewayModel()
	*cfg.Scaleway = *planned.Scaleway
	cfg.Scaleway.SecretKeyWO = types.StringValue("new")
	config := inventoryRaw(t, r, &cfg)
	resp := resource.UpdateResponse{State: state}
	r.Update(ctx, resource.UpdateRequest{
		State:  state,
		Plan:   tfsdk.Plan{Schema: plan.Schema, Raw: plan.Raw},
		Config: tfsdk.Config{Schema: config.Schema, Raw: config.Raw},
	}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("a refused update did not fail")
	}
	var got inventoryModel
	resp.State.Get(ctx, &got)
	if v := got.Scaleway.SecretKeyWOVersion.ValueInt64(); v != 1 {
		t.Errorf("state holds secret_key_wo_version %d after a refused update, want 1", v)
	}
	if n := f.count(getSync); n != 0 {
		t.Errorf("%d syncs after a refused update", n)
	}
}

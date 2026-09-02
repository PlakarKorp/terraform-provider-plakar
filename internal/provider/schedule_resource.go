// Copyright (c) 2026 PlakarKorp
// ISC License (see LICENSE)

package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/PlakarKorp/terraform-provider-plakar/internal/client"
)

// scheduleResource manages one scheduled task: a backup, prune, sync or
// check with its recurrence rules. A scheduled prune carries the retention
// rule — the retention policy, as code.
type scheduleResource struct {
	client *client.Client
}

type scheduleRuleModel struct {
	ID          types.String `tfsdk:"id"`
	Start       types.String `tfsdk:"start"`
	Periodicity types.Int64  `tfsdk:"periodicity"`
	Jitter      types.Int64  `tfsdk:"jitter"`
	Enabled     types.Bool   `tfsdk:"enabled"`
}

type scheduleModel struct {
	ID          types.String        `tfsdk:"id"`
	Name        types.String        `tfsdk:"name"`
	Description types.String        `tfsdk:"description"`
	Type        types.String        `tfsdk:"type"`
	OriginID    types.String        `tfsdk:"origin_id"`
	TargetID    types.String        `tfsdk:"target_id"`
	Enabled     types.Bool          `tfsdk:"enabled"`
	Rules       []scheduleRuleModel `tfsdk:"rule"`
	Labels      types.List          `tfsdk:"labels"`
	Ignores     types.List          `tfsdk:"ignores"`
	Retention   types.Map           `tfsdk:"retention"`
	GroupBy     types.String        `tfsdk:"group_by"`
}

func NewScheduleResource() resource.Resource {
	return &scheduleResource{}
}

func (r *scheduleResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_schedule"
}

func (r *scheduleResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A scheduled task — a backup, prune, sync or check with its " +
			"recurrence rules. A scheduled prune carries the retention rule.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required: true,
				Description: "Name of the schedule. Stored but not echoed by the " +
					"API, so drift on the name is not detected.",
			},
			"description": schema.StringAttribute{
				Optional:    true,
				Description: "Free-form description. Stored but not echoed by the API.",
			},
			"type": schema.StringAttribute{
				Required:    true,
				Description: "What the schedule runs: backup, prune, sync or check.",
				Validators: []validator.String{
					stringvalidator.OneOf("backup", "prune", "sync", "check"),
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"origin_id": schema.StringAttribute{
				Required: true,
				Description: "Id of the origin connector: the source for a backup, " +
					"the store for a prune, sync or check.",
			},
			"target_id": schema.StringAttribute{
				Optional: true,
				Description: "Id of the target connector: the store for a backup, " +
					"the destination store for a sync. A check has none.",
			},
			"enabled": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(true),
				Description: "Whether the schedule runs at all.",
			},
			"labels": schema.ListAttribute{
				Optional:    true,
				ElementType: types.StringType,
				Description: "For a backup: labels stamped on the snapshots. For the " +
					"other types: only snapshots carrying these tags are considered.",
			},
			"ignores": schema.ListAttribute{
				Optional:    true,
				ElementType: types.StringType,
				Description: "For a backup: path patterns to exclude.",
			},
			"retention": schema.MapAttribute{
				Optional:    true,
				ElementType: types.Int64Type,
				Description: "For a prune: the retention rule, as bucket options — " +
					"minute, hour, day, week, month, year for how many recent buckets " +
					"to keep, per_minute ... per_year for how many snapshots in each.",
			},
			"group_by": schema.StringAttribute{
				Optional: true,
				Description: "For a prune: partition matched snapshots before applying " +
					"the rule, e.g. dataset to hold the rule per source.",
				Validators: []validator.String{
					stringvalidator.OneOf("name", "category", "environment", "perimeter",
						"job", "dataset", "data-class", "tag", "origin", "type", "root"),
				},
			},
		},
		Blocks: map[string]schema.Block{
			"rule": schema.ListNestedBlock{
				Description: "A recurrence of the schedule. At least one.",
				NestedObject: schema.NestedBlockObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.StringAttribute{
							Computed:    true,
							Description: "Server-assigned rule id.",
						},
						"start": schema.StringAttribute{
							Optional:    true,
							Description: "RFC3339 time the rule starts from.",
						},
						"periodicity": schema.Int64Attribute{
							Required:    true,
							Description: "Seconds between runs.",
						},
						"jitter": schema.Int64Attribute{
							Optional:    true,
							Description: "Seconds of random spread around each run.",
						},
						"enabled": schema.BoolAttribute{
							Optional:    true,
							Computed:    true,
							Default:     booldefault.StaticBool(true),
							Description: "Whether this rule fires.",
						},
					},
				},
			},
		},
	}
}

func (r *scheduleResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("unexpected provider data", fmt.Sprintf("got %T", req.ProviderData))
		return
	}
	r.client = c
}

var retentionKeys = map[string]bool{
	"minute": true, "per_minute": true, "hour": true, "per_hour": true,
	"day": true, "per_day": true, "week": true, "per_week": true,
	"month": true, "per_month": true, "year": true, "per_year": true,
}

func (r *scheduleResource) taskRequestFromModel(ctx context.Context, m *scheduleModel, resp interface {
	AddError(summary, detail string)
}) *client.TaskRequest {
	config := client.TaskConfig{GroupBy: m.GroupBy.ValueString()}
	if !m.Labels.IsNull() && !m.Labels.IsUnknown() {
		_ = m.Labels.ElementsAs(ctx, &config.Labels, false)
	}
	if !m.Ignores.IsNull() && !m.Ignores.IsUnknown() {
		_ = m.Ignores.ElementsAs(ctx, &config.Ignores, false)
	}
	if !m.Retention.IsNull() && !m.Retention.IsUnknown() {
		retention := map[string]int64{}
		_ = m.Retention.ElementsAs(ctx, &retention, false)
		config.Retention = map[string]int{}
		for k, v := range retention {
			// The API silently drops unknown config keys; a typo'd bucket must
			// not quietly prune under a different rule than the config wrote.
			if !retentionKeys[k] {
				resp.AddError("invalid retention option",
					fmt.Sprintf("%q is not a retention bucket; valid: minute, per_minute, hour, per_hour, day, per_day, week, per_week, month, per_month, year, per_year", k))
				return nil
			}
			config.Retention[k] = int(v)
		}
	}

	rules := make([]client.ScheduleRule, 0, len(m.Rules))
	for i := range m.Rules {
		rule := client.ScheduleRule{Enabled: m.Rules[i].Enabled.ValueBool()}
		if !m.Rules[i].ID.IsNull() && !m.Rules[i].ID.IsUnknown() {
			id := m.Rules[i].ID.ValueString()
			rule.ID = &id
		}
		if !m.Rules[i].Start.IsNull() {
			start := m.Rules[i].Start.ValueString()
			rule.Start = &start
		}
		if !m.Rules[i].Periodicity.IsNull() {
			p := m.Rules[i].Periodicity.ValueInt64()
			rule.Periodicity = &p
		}
		if !m.Rules[i].Jitter.IsNull() {
			j := m.Rules[i].Jitter.ValueInt64()
			rule.Jitter = &j
		}
		rules = append(rules, rule)
	}

	return client.NewTaskRequest(
		m.Name.ValueString(), m.Description.ValueString(), m.Type.ValueString(),
		m.OriginID.ValueString(), m.TargetID.ValueString(),
		client.Schedule{Enabled: m.Enabled.ValueBool(), Rules: rules},
		config,
	)
}

// refreshFromTask maps a stored task back onto the model. Name and
// description are not echoed by the API and keep their state values.
func (r *scheduleResource) refreshFromTask(ctx context.Context, m *scheduleModel, task *client.Task, resp interface {
	AddError(summary, detail string)
}) {
	m.Type = types.StringValue(task.Type)
	if task.Origin != nil {
		m.OriginID = types.StringValue(task.Origin.ID)
	}
	if task.Target != nil {
		m.TargetID = types.StringValue(task.Target.ID)
	} else {
		m.TargetID = types.StringNull()
	}
	m.Enabled = types.BoolValue(task.Schedule.Enabled)

	rules := make([]scheduleRuleModel, 0, len(task.Schedule.Rules))
	for _, rule := range task.Schedule.Rules {
		rm := scheduleRuleModel{
			ID:      types.StringNull(),
			Start:   types.StringNull(),
			Enabled: types.BoolValue(rule.Enabled),
		}
		if rule.ID != nil {
			rm.ID = types.StringValue(*rule.ID)
		}
		if rule.Start != nil {
			rm.Start = types.StringValue(*rule.Start)
		}
		if rule.Periodicity != nil {
			rm.Periodicity = types.Int64Value(*rule.Periodicity)
		}
		if rule.Jitter != nil {
			rm.Jitter = types.Int64Value(*rule.Jitter)
		} else {
			rm.Jitter = types.Int64Null()
		}
		rules = append(rules, rm)
	}
	m.Rules = rules

	labels, ignores, retention, groupBy, err := task.ParsedConfig()
	if err != nil {
		resp.AddError("decoding schedule config", err.Error())
		return
	}
	if !m.Labels.IsNull() || len(labels) > 0 {
		list, _ := types.ListValueFrom(ctx, types.StringType, orEmpty(labels))
		m.Labels = list
	}
	if !m.Ignores.IsNull() || len(ignores) > 0 {
		list, _ := types.ListValueFrom(ctx, types.StringType, orEmpty(ignores))
		m.Ignores = list
	}
	if !m.Retention.IsNull() || len(retention) > 0 {
		mv, _ := types.MapValueFrom(ctx, types.Int64Type, retention)
		m.Retention = mv
	}
	if groupBy != "" {
		m.GroupBy = types.StringValue(groupBy)
	} else if !m.GroupBy.IsNull() {
		m.GroupBy = types.StringNull()
	}
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func (r *scheduleResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan scheduleModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	treq := r.taskRequestFromModel(ctx, &plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	created, err := r.client.CreateTask(treq)
	if err != nil {
		resp.Diagnostics.AddError("creating schedule", err.Error())
		return
	}
	plan.ID = types.StringValue(created.ID)
	// Read back: rule ids are server-assigned, and the server may normalize
	// what it stored.
	task, err := r.client.GetTask(created.ID)
	if err != nil {
		resp.Diagnostics.AddError("reading schedule after create", err.Error())
		return
	}
	r.refreshFromTask(ctx, &plan, task, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *scheduleResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state scheduleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	task, err := r.client.GetTask(state.ID.ValueString())
	if err != nil {
		if client.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("reading schedule", err.Error())
		return
	}
	r.refreshFromTask(ctx, &state, task, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *scheduleResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state scheduleModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	treq := r.taskRequestFromModel(ctx, &plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.UpdateTask(state.ID.ValueString(), treq); err != nil {
		resp.Diagnostics.AddError("updating schedule", err.Error())
		return
	}
	plan.ID = state.ID
	task, err := r.client.GetTask(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("reading schedule after update", err.Error())
		return
	}
	r.refreshFromTask(ctx, &plan, task, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *scheduleResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state scheduleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteTask(state.ID.ValueString()); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("deleting schedule", err.Error())
	}
}

func (r *scheduleResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

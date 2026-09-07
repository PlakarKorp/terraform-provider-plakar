// Copyright (c) 2026 PlakarKorp
// ISC License (see LICENSE)

package provider

import (
	"context"
	"os"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/PlakarKorp/terraform-provider-plakar/internal/client"
)

type plakarProvider struct {
	version string
}

type plakarProviderModel struct {
	APIURL         types.String `tfsdk:"api_url"`
	APIKey         types.String `tfsdk:"api_key"`
	OrganizationID types.String `tfsdk:"organization_id"`
}

func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &plakarProvider{version: version}
	}
}

func (p *plakarProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "plakar"
	resp.Version = p.version
}

func (p *plakarProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manage Plakar backup configuration — stores, connectors and schedules — through the Plakar management API.",
		Attributes: map[string]schema.Attribute{
			"api_url": schema.StringAttribute{
				Optional:    true,
				Description: "Base URL of the Plakar management API, e.g. https://plakar.example.com. Falls back to PLAKAR_API_URL.",
			},
			"api_key": schema.StringAttribute{
				Optional:    true,
				Sensitive:   true,
				Description: "API key of a service account (pcp_ak_...). Falls back to PLAKAR_API_KEY.",
			},
			"organization_id": schema.StringAttribute{
				Optional:    true,
				Description: "Organization to operate in, when different from the one the API key is bound to.",
			},
		},
	}
}

func (p *plakarProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var config plakarProviderModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	apiURL := config.APIURL.ValueString()
	if apiURL == "" {
		apiURL = os.Getenv("PLAKAR_API_URL")
	}
	apiKey := config.APIKey.ValueString()
	if apiKey == "" {
		apiKey = os.Getenv("PLAKAR_API_KEY")
	}
	if apiURL == "" {
		resp.Diagnostics.AddError("missing api_url",
			"Set the provider's api_url or the PLAKAR_API_URL environment variable.")
	}
	if apiKey == "" {
		resp.Diagnostics.AddError("missing api_key",
			"Set the provider's api_key or the PLAKAR_API_KEY environment variable.")
	}
	if resp.Diagnostics.HasError() {
		return
	}

	c := client.New(apiURL, apiKey, config.OrganizationID.ValueString())
	resp.ResourceData = c
	resp.DataSourceData = c
}

func (p *plakarProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewStoreResource,
		NewConnectorResource,
		NewScheduleResource,
		NewInventoryResource,
		NewInventoryResourceEntry,
		NewOrganizationResource,
		NewMemberResource,
		NewGrantResource,
	}
}

func (p *plakarProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewResourceDataSource,
		NewIntegrationDataSource,
		NewStoreDataSource,
		NewConnectorDataSource,
		NewInventoryDataSource,
		NewOrganizationDataSource,
		NewMemberDataSource,
	}
}

package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/Staffbase/terraform-provider-truefoundry/internal/client"
)

var (
	_ datasource.DataSource              = (*modelsDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*modelsDataSource)(nil)
)

func newModelsDataSource() datasource.DataSource {
	return &modelsDataSource{}
}

// modelsDataSource lists model integrations available on the AI Gateway via
// GET /api/svc/v1/provider-integrations?type=model.
type modelsDataSource struct {
	client *client.Client
}

type modelsDataSourceModel struct {
	ProviderAccountFQN types.String           `tfsdk:"provider_account_fqn"`
	Models             []modelDataSourceModel `tfsdk:"models"`
}

type modelDataSourceModel struct {
	ID                 types.String   `tfsdk:"id"`
	Name               types.String   `tfsdk:"name"`
	FQN                types.String   `tfsdk:"fqn"`
	ModelID            types.String   `tfsdk:"model_id"`
	ModelTypes         []types.String `tfsdk:"model_types"`
	ProviderAccountFQN types.String   `tfsdk:"provider_account_fqn"`
}

func (d *modelsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_models"
}

func (d *modelsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Lists the models available on the TrueFoundry AI Gateway.",
		Attributes: map[string]schema.Attribute{
			"provider_account_fqn": schema.StringAttribute{
				Optional:    true,
				Description: "Only return models of the provider account with this FQN.",
			},
			"models": schema.ListNestedAttribute{
				Computed:    true,
				Description: "Model integrations registered on the gateway.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":                   schema.StringAttribute{Computed: true, Description: "Integration ID."},
						"name":                 schema.StringAttribute{Computed: true, Description: "Integration name."},
						"fqn":                  schema.StringAttribute{Computed: true, Description: "Fully qualified name of the model integration."},
						"model_id":             schema.StringAttribute{Computed: true, Description: "Provider model identifier."},
						"model_types":          schema.ListAttribute{Computed: true, ElementType: types.StringType, Description: "Model capabilities."},
						"provider_account_fqn": schema.StringAttribute{Computed: true, Description: "FQN of the provider account the model belongs to."},
					},
				},
			},
		},
	}
}

func (d *modelsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("expected *client.Client, got %T", req.ProviderData))
		return
	}
	d.client = c
}

func (d *modelsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config modelsDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	integrations, err := d.client.ListModelIntegrations(ctx, "")
	if err != nil {
		resp.Diagnostics.AddError("Listing TrueFoundry models failed", err.Error())
		return
	}

	config.Models = []modelDataSourceModel{}
	for _, integration := range integrations {
		if filter := config.ProviderAccountFQN.ValueString(); filter != "" && integration.ProviderAccountFQN != filter {
			continue
		}
		m := modelDataSourceModel{
			ID:                 types.StringValue(integration.ID),
			Name:               types.StringValue(integration.Name),
			FQN:                types.StringValue(integration.FQN),
			ModelID:            types.StringValue(integration.Manifest.ModelID),
			ProviderAccountFQN: types.StringValue(integration.ProviderAccountFQN),
		}
		for _, t := range integration.Manifest.ModelTypes {
			m.ModelTypes = append(m.ModelTypes, types.StringValue(t))
		}
		config.Models = append(config.Models, m)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}

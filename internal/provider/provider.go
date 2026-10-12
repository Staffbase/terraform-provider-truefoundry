// Package provider implements the TrueFoundry Terraform provider.
package provider

import (
	"context"
	"os"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/Staffbase/terraform-provider-truefoundry/internal/client"
)

// Ensure the implementation satisfies the framework interfaces.
var _ provider.Provider = (*TrueFoundryProvider)(nil)

// TrueFoundryProvider authenticates against a TrueFoundry control plane with
// an API token and exposes resources to manage AI Gateway models.
type TrueFoundryProvider struct {
	version string
}

// New returns a provider factory for the given version.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &TrueFoundryProvider{version: version}
	}
}

type providerModel struct {
	Host   types.String `tfsdk:"host"`
	APIKey types.String `tfsdk:"api_key"`
}

func (p *TrueFoundryProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "truefoundry"
	resp.Version = p.version
}

func (p *TrueFoundryProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manage TrueFoundry AI Gateway resources on a control plane, authenticated with an API token.",
		Attributes: map[string]schema.Attribute{
			"host": schema.StringAttribute{
				Optional: true,
				Description: "Base URL of the TrueFoundry control plane, e.g. https://my-org.truefoundry.cloud. " +
					"Can also be set via the TFY_HOST environment variable.",
			},
			"api_key": schema.StringAttribute{
				Optional:  true,
				Sensitive: true,
				Description: "TrueFoundry API key (personal access token or virtual account token). " +
					"Can also be set via the TFY_API_KEY environment variable.",
			},
		},
	}
}

func (p *TrueFoundryProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var config providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	host := config.Host.ValueString()
	if host == "" {
		host = os.Getenv("TFY_HOST")
	}
	apiKey := config.APIKey.ValueString()
	if apiKey == "" {
		apiKey = os.Getenv("TFY_API_KEY")
	}

	if host == "" {
		resp.Diagnostics.AddError(
			"Missing TrueFoundry control plane host",
			"Set the provider attribute `host` or the TFY_HOST environment variable to the control plane base URL.",
		)
	}
	if apiKey == "" {
		resp.Diagnostics.AddError(
			"Missing TrueFoundry API key",
			"Set the provider attribute `api_key` or the TFY_API_KEY environment variable.",
		)
	}
	if resp.Diagnostics.HasError() {
		return
	}

	c, err := client.New(host, apiKey, "terraform-provider-truefoundry/"+p.version)
	if err != nil {
		resp.Diagnostics.AddError("Invalid TrueFoundry control plane host", err.Error())
		return
	}
	resp.ResourceData = c
	resp.DataSourceData = c
}

func (p *TrueFoundryProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		newProviderAccountResource,
		newVirtualAccountResource,
		newGuardrailGroupResource,
		newCustomEndpointAccountResource,
		newAzureAccountResource,
		newSlackAccountResource,
		newHashicorpAccountResource,
		newSystemSecretStoreResource,
		newGatewayBudgetConfigResource,
		newTenantBudgetConfigResource,
		newTeamBudgetConfigResource,
		newGatewayDataAccessConfigResource,
		newGatewayDataRoutingConfigResource,
		newGatewayGuardrailsConfigResource,
		newGatewayLoadBalancingConfigResource,
		newGatewayOtelConfigResource,
		newGatewayRateLimitingConfigResource,
		newGatewayGlobalSettingsResource,
		newRoleResource,
		newRoleBindingResource,
		newSecretGroupResource,
		newSecretResource,
		newGatewayInstallationResource,
		newMcpServerResource,
		newOpenapiMcpServerResource,
		newVirtualMcpServerResource,
		newAgentResource,
		newRemoteAgentResource,
		newMlRepoResource,
		newAgentSkillResource,
		newPromptResource,
	}
}

func (p *TrueFoundryProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		newModelsDataSource,
	}
}

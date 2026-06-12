package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ datasource.DataSource              = (*gitFileDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*gitFileDataSource)(nil)
)

func NewGitFileDataSource() datasource.DataSource {
	return &gitFileDataSource{}
}

type gitFileDataSource struct {
	config *providerConfig
}

type gitFileDataSourceModel struct {
	RepositoryURL types.String `tfsdk:"repository_url"`
	Branch        types.String `tfsdk:"branch"`
	Path          types.String `tfsdk:"path"`
	Content       types.String `tfsdk:"content"`
	CommitSHA     types.String `tfsdk:"commit_sha"`
	BlobSHA       types.String `tfsdk:"blob_sha"`
}

func (d *gitFileDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_file"
}

func (d *gitFileDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads one text file from a Git repository branch.",
		Attributes: map[string]schema.Attribute{
			"repository_url": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Git repository URL.",
			},
			"branch": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Branch to read from.",
			},
			"path": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Repository-relative file path.",
			},
			"content": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Text file content.",
			},
			"commit_sha": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Remote branch commit SHA used for the read.",
			},
			"blob_sha": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Git blob SHA for the file content.",
			},
		},
	}
}

func (d *gitFileDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	cfg, ok := req.ProviderData.(*providerConfig)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("Expected *providerConfig, got %T", req.ProviderData))
		return
	}
	d.config = cfg
}

func (d *gitFileDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var model gitFileDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if d.config == nil {
		resp.Diagnostics.AddError("Provider not configured", "The provider configuration was not available to the git_file data source.")
		return
	}

	info, err := d.config.Manager.ReadFile(model.RepositoryURL.ValueString(), model.Branch.ValueString(), model.Path.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Read git_file data source failed", err.Error())
		return
	}
	model.Content = types.StringValue(info.Content)
	model.CommitSHA = types.StringValue(info.CommitSHA)
	model.BlobSHA = types.StringValue(info.BlobSHA)
	resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
}

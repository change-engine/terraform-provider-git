package provider

import (
	"context"
	"errors"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource              = (*gitFileResource)(nil)
	_ resource.ResourceWithConfigure = (*gitFileResource)(nil)
)

func NewGitFileResource() resource.Resource {
	return &gitFileResource{}
}

type gitFileResource struct {
	config *providerConfig
}

type gitFileResourceModel struct {
	RepositoryURL       types.String `tfsdk:"repository_url"`
	Branch              types.String `tfsdk:"branch"`
	Path                types.String `tfsdk:"path"`
	Content             types.String `tfsdk:"content"`
	CommitMessage       types.String `tfsdk:"commit_message"`
	AuthorName          types.String `tfsdk:"author_name"`
	AuthorEmail         types.String `tfsdk:"author_email"`
	ID                  types.String `tfsdk:"id"`
	CommitSHA           types.String `tfsdk:"commit_sha"`
	BlobSHA             types.String `tfsdk:"blob_sha"`
	LastRemoteCommitSHA types.String `tfsdk:"last_remote_commit_sha"`
}

func (r *gitFileResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_file"
}

func (r *gitFileResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages one text file in a Git repository branch.",
		Attributes: map[string]schema.Attribute{
			"repository_url": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Git repository URL.",
			},
			"branch": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Branch to read from and push to.",
			},
			"path": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Repository-relative file path.",
			},
			"content": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Text file content.",
			},
			"commit_message": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Commit message for create, update, and delete operations. Defaults to an operation-specific provider-generated message.",
			},
			"author_name": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Git author name for commits created by this resource.",
			},
			"author_email": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Git author email for commits created by this resource.",
			},
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Stable resource identifier derived from repository URL, branch, and path.",
			},
			"commit_sha": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Commit SHA at which the managed file content was last observed or written.",
			},
			"blob_sha": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Git blob SHA for the managed file content.",
			},
			"last_remote_commit_sha": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Remote branch commit SHA observed immediately before the most recent operation.",
			},
		},
	}
}

func (r *gitFileResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	cfg, ok := req.ProviderData.(*providerConfig)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("Expected *providerConfig, got %T", req.ProviderData))
		return
	}
	r.config = cfg
}

func (r *gitFileResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan gitFileResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !r.ensureConfigured(&resp.Diagnostics) {
		return
	}

	result, err := r.config.Manager.WriteFile(
		plan.RepositoryURL.ValueString(),
		plan.Branch.ValueString(),
		plan.Path.ValueString(),
		plan.Content.ValueString(),
		nil,
		r.commitOptions(plan, "Create "+plan.Path.ValueString()),
	)
	if err != nil {
		resp.Diagnostics.AddError("Create git_file failed", err.Error())
		return
	}
	setResourceComputed(&plan, result.CommitSHA, result.BlobSHA, result.LastRemoteCommitSHA)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *gitFileResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state gitFileResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !r.ensureConfigured(&resp.Diagnostics) {
		return
	}

	info, err := r.config.Manager.ReadFile(state.RepositoryURL.ValueString(), state.Branch.ValueString(), state.Path.ValueString())
	if err != nil {
		if errors.Is(err, errGitFileNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddAttributeError(path.Root("path"), "Read git_file failed", err.Error())
		return
	}
	state.Content = types.StringValue(info.Content)
	state.CommitSHA = types.StringValue(info.CommitSHA)
	state.BlobSHA = types.StringValue(info.BlobSHA)
	state.LastRemoteCommitSHA = types.StringValue(info.CommitSHA)
	state.ID = types.StringValue(resourceID(state.RepositoryURL.ValueString(), state.Branch.ValueString(), state.Path.ValueString()))
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *gitFileResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan gitFileResourceModel
	var state gitFileResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !r.ensureConfigured(&resp.Diagnostics) {
		return
	}

	expected := state.Content.ValueString()
	result, err := r.config.Manager.WriteFile(
		plan.RepositoryURL.ValueString(),
		plan.Branch.ValueString(),
		plan.Path.ValueString(),
		plan.Content.ValueString(),
		&expected,
		r.commitOptions(plan, "Update "+plan.Path.ValueString()),
	)
	if err != nil {
		resp.Diagnostics.AddError("Update git_file failed", err.Error())
		return
	}
	setResourceComputed(&plan, result.CommitSHA, result.BlobSHA, result.LastRemoteCommitSHA)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *gitFileResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state gitFileResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !r.ensureConfigured(&resp.Diagnostics) {
		return
	}

	expected := state.Content.ValueString()
	_, err := r.config.Manager.DeleteFile(
		state.RepositoryURL.ValueString(),
		state.Branch.ValueString(),
		state.Path.ValueString(),
		&expected,
		r.commitOptions(state, "Delete "+state.Path.ValueString()),
	)
	if err != nil {
		resp.Diagnostics.AddError("Delete git_file failed", err.Error())
		return
	}
	resp.State.RemoveResource(ctx)
}

func (r *gitFileResource) commitOptions(model gitFileResourceModel, defaultMessage string) commitOptions {
	message := stringValue(model.CommitMessage)
	if message == "" {
		message = r.config.CommitMessagePrefix + defaultMessage
	}
	return commitOptions{
		Message:     message,
		AuthorName:  firstNonEmpty(stringValue(model.AuthorName), r.config.AuthorName),
		AuthorEmail: firstNonEmpty(stringValue(model.AuthorEmail), r.config.AuthorEmail),
	}
}

func (r *gitFileResource) ensureConfigured(diags *diag.Diagnostics) bool {
	if r.config != nil {
		return true
	}
	diags.AddError("Provider not configured", "The provider configuration was not available to the git_file resource.")
	return false
}

func setResourceComputed(model *gitFileResourceModel, commitSHA, blobSHA, lastRemoteCommitSHA string) {
	model.ID = types.StringValue(resourceID(model.RepositoryURL.ValueString(), model.Branch.ValueString(), model.Path.ValueString()))
	model.CommitSHA = types.StringValue(commitSHA)
	model.BlobSHA = types.StringValue(blobSHA)
	model.LastRemoteCommitSHA = types.StringValue(lastRemoteCommitSHA)
}

func resourceID(repositoryURL, branch, repoPath string) string {
	return repositoryURL + "|" + branch + "|" + repoPath
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

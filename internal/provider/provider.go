package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ provider.Provider = (*gitProvider)(nil)

func New() provider.Provider {
	return &gitProvider{}
}

type gitProvider struct{}

type providerModel struct {
	CacheDir              types.String `tfsdk:"cache_dir"`
	Username              types.String `tfsdk:"username"`
	Token                 types.String `tfsdk:"token"`
	SSHPrivateKey         types.String `tfsdk:"ssh_private_key"`
	SSHPassphrase         types.String `tfsdk:"ssh_passphrase"`
	KnownHostsFile        types.String `tfsdk:"known_hosts_file"`
	InsecureIgnoreHostKey types.Bool   `tfsdk:"insecure_ignore_host_key"`
	AuthorName            types.String `tfsdk:"author_name"`
	AuthorEmail           types.String `tfsdk:"author_email"`
	CommitMessagePrefix   types.String `tfsdk:"commit_message_prefix"`
}

type providerConfig struct {
	CacheDir            string
	Auth                authConfig
	AuthorName          string
	AuthorEmail         string
	CommitMessagePrefix string
	Manager             *cloneManager
}

type authConfig struct {
	Username              string
	Token                 string
	SSHPrivateKey         string
	SSHPassphrase         string
	KnownHostsFile        string
	InsecureIgnoreHostKey bool
}

func (p *gitProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "git"
}

func (p *gitProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manage files in Git repositories.",
		Attributes: map[string]schema.Attribute{
			"cache_dir": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Directory used for provider-managed repository clones. Defaults to the OS user cache directory under `terraform-provider-git`.",
			},
			"username": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Username for HTTPS authentication.",
			},
			"token": schema.StringAttribute{
				Optional:            true,
				Sensitive:           true,
				MarkdownDescription: "Token or password for HTTPS authentication.",
			},
			"ssh_private_key": schema.StringAttribute{
				Optional:            true,
				Sensitive:           true,
				MarkdownDescription: "PEM encoded SSH private key.",
			},
			"ssh_passphrase": schema.StringAttribute{
				Optional:            true,
				Sensitive:           true,
				MarkdownDescription: "Passphrase for the SSH private key.",
			},
			"known_hosts_file": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Path to an OpenSSH known_hosts file used for SSH host key verification.",
			},
			"insecure_ignore_host_key": schema.BoolAttribute{
				Optional:            true,
				MarkdownDescription: "Disable SSH host key verification. This is insecure and should only be used for controlled test environments.",
			},
			"author_name": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Default Git author name for commits created by resources.",
			},
			"author_email": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Default Git author email for commits created by resources.",
			},
			"commit_message_prefix": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Prefix added to default commit messages.",
			},
		},
	}
}

func (p *gitProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var model providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}

	cfg, err := configFromModel(model)
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("token"), "Invalid provider configuration", err.Error())
		return
	}

	resp.ResourceData = cfg
	resp.DataSourceData = cfg
}

func (p *gitProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewGitFileResource,
	}
}

func (p *gitProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewGitFileDataSource,
	}
}

func configFromModel(model providerModel) (*providerConfig, error) {
	cfg := &providerConfig{
		CacheDir:            stringValue(model.CacheDir),
		AuthorName:          stringValue(model.AuthorName),
		AuthorEmail:         stringValue(model.AuthorEmail),
		CommitMessagePrefix: stringValue(model.CommitMessagePrefix),
		Auth:                authConfig{},
	}
	cfg.Auth.Username = stringValue(model.Username)
	cfg.Auth.Token = stringValue(model.Token)
	cfg.Auth.SSHPrivateKey = stringValue(model.SSHPrivateKey)
	cfg.Auth.SSHPassphrase = stringValue(model.SSHPassphrase)
	cfg.Auth.KnownHostsFile = stringValue(model.KnownHostsFile)
	cfg.Auth.InsecureIgnoreHostKey = boolValue(model.InsecureIgnoreHostKey)

	if cfg.CacheDir == "" {
		userCacheDir, err := os.UserCacheDir()
		if err != nil {
			return nil, fmt.Errorf("determine default cache directory: %w", err)
		}
		cfg.CacheDir = filepath.Join(userCacheDir, "terraform-provider-git")
	}

	if err := validateAuthConfig(cfg.Auth); err != nil {
		return nil, err
	}

	cfg.Manager = newCloneManager(cfg.CacheDir, cfg.Auth)
	return cfg, nil
}

func validateAuthConfig(auth authConfig) error {
	hasHTTP := auth.Username != "" || auth.Token != ""
	hasSSH := auth.SSHPrivateKey != "" || auth.SSHPassphrase != ""
	if hasHTTP && hasSSH {
		return fmt.Errorf("HTTPS authentication and SSH private key authentication are mutually exclusive")
	}
	if auth.Token != "" && auth.Username == "" {
		return fmt.Errorf("username must be configured when token is configured")
	}
	if auth.Username != "" && auth.Token == "" {
		return fmt.Errorf("token must be configured when username is configured")
	}
	if auth.SSHPassphrase != "" && auth.SSHPrivateKey == "" {
		return fmt.Errorf("ssh_private_key must be configured when ssh_passphrase is configured")
	}
	if (auth.KnownHostsFile != "" || auth.InsecureIgnoreHostKey) && auth.SSHPrivateKey == "" {
		return fmt.Errorf("ssh_private_key must be configured when SSH host key options are configured")
	}
	if auth.KnownHostsFile != "" && auth.InsecureIgnoreHostKey {
		return fmt.Errorf("known_hosts_file and insecure_ignore_host_key are mutually exclusive")
	}
	return nil
}

func (a authConfig) fingerprint() string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(a.Username))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(a.Token))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(a.SSHPrivateKey))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(a.SSHPassphrase))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(a.KnownHostsFile))
	_, _ = hash.Write([]byte{0})
	if a.InsecureIgnoreHostKey {
		_, _ = hash.Write([]byte{1})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func stringValue(value types.String) string {
	if value.IsNull() || value.IsUnknown() {
		return ""
	}
	return value.ValueString()
}

func boolValue(value types.Bool) bool {
	if value.IsNull() || value.IsUnknown() {
		return false
	}
	return value.ValueBool()
}

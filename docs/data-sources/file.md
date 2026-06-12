# git_file Data Source

Reads one text file from a Git repository branch.

## Example

```hcl
data "git_file" "config" {
  repository_url = "https://github.com/example/repo.git"
  branch         = "main"
  path           = "config/app.txt"
}

output "config_content" {
  value = data.git_file.config.content
}
```

## Schema

Required:

- `repository_url` - Git repository URL.
- `branch` - Branch to read from.
- `path` - Repository-relative file path.

Computed:

- `content` - Text file content.
- `commit_sha` - Remote branch commit SHA used for the read.
- `blob_sha` - Git blob SHA for the file content.


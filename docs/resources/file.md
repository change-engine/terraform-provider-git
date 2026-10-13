# git_file Resource

Manages one text file in a Git repository branch. Create, update, and delete each create one commit and push it to the remote branch. Refresh reads remote content into Terraform state. Apply-time updates and deletes refuse to proceed if the remote file has changed since the state OpenTofu planned from. Creates refuse to replace a file that already exists with different content unless `overwrite_on_create` is set; this matters when a resource drops out of state, for example because its file moved, and `lifecycle { ignore_changes }` would otherwise not stop the file being rewritten.

## Example

```hcl
resource "git_file" "config" {
  repository_url = "https://github.com/example/repo.git"
  branch         = "main"
  path           = "config/app.txt"
  content        = "enabled=true\n"

  commit_message = "Update app config"
  author_name    = "Terraform"
  author_email   = "terraform@example.com"
}
```

## Schema

Required:

- `repository_url` - Git repository URL.
- `branch` - Branch to read from and push to.
- `path` - Repository-relative file path.
- `content` - Text file content.

Optional:

- `commit_message` - Commit message for resource operations.
- `author_name` - Commit author name override.
- `author_email` - Commit author email override.
- `overwrite_on_create` - Replace an existing file with different content on create. Defaults to `false`.

Computed:

- `id` - Resource identifier.
- `commit_sha` - Commit SHA at which the file content was last observed or written.
- `blob_sha` - Git blob SHA for the file content.
- `last_remote_commit_sha` - Remote branch commit SHA observed immediately before the most recent operation.

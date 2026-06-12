# Git Provider

The Git provider manages individual text files in Git repositories. Each `git_file` resource manages exactly one repository-relative path; manage multiple files with multiple resources.

## Provider Configuration

```hcl
provider "git" {
  cache_dir = "/tmp/terraform-provider-git"

  author_name             = "Terraform"
  author_email            = "terraform@example.com"
  commit_message_prefix   = "[terraform] "
}
```

## Authentication

HTTPS authentication:

```hcl
provider "git" {
  username = "git"
  token    = var.git_token
}
```

SSH authentication:

```hcl
provider "git" {
  ssh_private_key   = file("~/.ssh/id_ed25519")
  known_hosts_file  = "~/.ssh/known_hosts"
}
```

`insecure_ignore_host_key = true` is available for controlled test environments and is mutually exclusive with `known_hosts_file`.


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

## Staging Changes on a Branch

Set `push_branch` to push every commit to one branch instead of each resource's `branch`, for example to review the changes as a pull request:

```hcl
provider "git" {
  push_branch = "terraform/sync"
}
```

- While `push_branch` exists on the remote, files are read from it and commits are added to it.
- When it does not exist, files are read from the resource's `branch` and the first write creates `push_branch` from it.
- Once the branch is merged and deleted, reads fall back to `branch`, so merged content plans as unchanged.
- To discard staged changes, delete the branch; the next write starts again from `branch`.

A push rejected because the remote branch moved is retried on a fresh fetch, re-checking each file for drift. This applies with or without `push_branch`.

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


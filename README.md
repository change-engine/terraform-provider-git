# Terraform Provider Git

Terraform provider scaffold for managing individual text files in Git repositories.

The provider uses the Terraform Plugin Framework and `github.com/go-git/go-git/v6` for native Git operations.

## Example

```hcl
terraform {
  required_providers {
    git = {
      source = "change-engine/git"
    }
  }
}

provider "git" {
  author_name  = "Terraform"
  author_email = "terraform@example.com"
}

resource "git_file" "example" {
  repository_url = "https://github.com/example/repo.git"
  branch         = "main"
  path           = "managed/example.txt"
  content        = "Managed by Terraform\n"
}
```

## Development

Run tests with:

```shell
CGO_ENABLED=0 GOCACHE=/tmp/go-cache go test ./...
```


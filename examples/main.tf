terraform {
  required_providers {
    git = {
      source = "change-engine/git"
    }
  }
}

variable "repository_url" {
  type = string
}

provider "git" {
  author_name           = "Terraform"
  author_email          = "terraform@example.com"
  commit_message_prefix = "[terraform] "
}

resource "git_file" "example" {
  repository_url = var.repository_url
  branch         = "main"
  path           = "managed/example.txt"
  content        = "Managed by Terraform\n"
}

data "git_file" "example" {
  repository_url = var.repository_url
  branch         = "main"
  path           = git_file.example.path
}


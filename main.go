package main

import (
	"context"
	"log"

	"github.com/change-engine/terraform-provider-git/internal/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
)

func main() {
	err := providerserver.Serve(context.Background(), provider.New, providerserver.ServeOpts{
		Address: "registry.terraform.io/change-engine/git",
	})
	if err != nil {
		log.Fatal(err)
	}
}

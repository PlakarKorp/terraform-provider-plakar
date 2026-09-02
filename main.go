// Copyright (c) 2026 PlakarKorp
// ISC License (see LICENSE)

package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	"github.com/PlakarKorp/terraform-provider-plakar/internal/provider"
)

// version is set by goreleaser at build time.
var version = "dev"

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "run with support for debuggers like delve")
	flag.Parse()

	err := providerserver.Serve(context.Background(), provider.New(version), providerserver.ServeOpts{
		Address: "registry.terraform.io/plakarkorp/plakar",
		Debug:   debug,
	})
	if err != nil {
		log.Fatal(err)
	}
}

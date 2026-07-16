// terraform-provider-truefoundry is a Terraform provider for the TrueFoundry
// AI platform (spike: AI Gateway model management).
package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	"github.com/Staffbase/terraform-provider-truefoundry/internal/provider"
)

// version is set by goreleaser via ldflags on release builds.
var version = "dev"

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "run the provider with support for debuggers like delve")
	flag.Parse()

	err := providerserver.Serve(context.Background(), provider.New(version), providerserver.ServeOpts{
		Address: "registry.terraform.io/staffbase/truefoundry",
		Debug:   debug,
	})
	if err != nil {
		log.Fatal(err)
	}
}

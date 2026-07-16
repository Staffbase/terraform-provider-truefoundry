// mock-control-plane serves an in-memory mock of the TrueFoundry control
// plane API for manually exercising the provider without real credentials:
//
//	go run ./cmd/mock-control-plane -addr :8330 -api-key local-token
package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/Staffbase/terraform-provider-truefoundry/internal/mockcp"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8330", "listen address")
	apiKey := flag.String("api-key", "local-token", "API key the mock control plane accepts")
	flag.Parse()

	log.Printf("mock TrueFoundry control plane listening on http://%s (API key %q)", *addr, *apiKey)
	log.Fatal(http.ListenAndServe(*addr, mockcp.New(*apiKey).Handler()))
}

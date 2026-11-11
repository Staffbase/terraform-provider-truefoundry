// Copyright 2026, Staffbase SE and contributors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
//
// You may obtain a copy of the License at
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

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

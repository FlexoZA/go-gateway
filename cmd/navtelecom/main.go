// Command navtelecom runs the navtelecom unit-type gateway server.
//
// One binary, one unit type. All the gateway wiring (config, Postgres registry,
// telemetry webhook, HTTP API, live settings, …) lives in internal/core/app; this
// main just selects the navtelecom protocol. Implement the wire format in
// internal/navtelecom/protocol.go.
package main

import (
	"github.com/dfm/device-gateway/internal/core/app"
	"github.com/dfm/device-gateway/internal/navtelecom"
)

func main() { app.Run(navtelecom.New()) }

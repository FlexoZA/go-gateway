// Command fleetiger runs the FleeTiger (Concox GT06) GPS tracker unit-type
// gateway server.
//
// One binary, one unit type. All the gateway wiring (config, Postgres registry,
// telemetry webhook, HTTP API, live settings, …) lives in internal/core/app; this
// main just selects the fleetiger protocol. The wire format is implemented in
// internal/fleetiger.
package main

import (
	"github.com/dfm/device-gateway/internal/core/app"
	"github.com/dfm/device-gateway/internal/fleetiger"
)

func main() { app.Run(fleetiger.New()) }

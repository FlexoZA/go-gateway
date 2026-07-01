// Command jt808 runs the jt808 unit-type gateway server.
//
// One binary, one unit type. All the gateway wiring (config, Postgres registry,
// telemetry webhook, HTTP API, live settings, …) lives in internal/core/app; this
// main just selects the jt808 protocol. Implement the wire format in
// internal/jt808/protocol.go.
package main

import (
	"github.com/dfm/device-gateway/internal/core/app"
	"github.com/dfm/device-gateway/internal/jt808"
)

func main() { app.Run(jt808.New(jt808.N62())) }

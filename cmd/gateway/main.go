// Command gateway runs the multi-unit device gateway: one process hosting every
// registered unit type, each on its own TCP listener/port, behind one shared
// Postgres registry, telemetry webhook, HTTP API, and admin panel.
//
// All the gateway wiring (config, registry, webhook, HTTP API, live settings,
// per-unit settings, media) lives in internal/core/app. Registering a unit is a
// single line below; removing one is deleting that line. Each unit's wire format
// lives in internal/<unit>.
package main

import (
	"github.com/dfm/device-gateway/internal/cathexis"
	"github.com/dfm/device-gateway/internal/core/app"
	"github.com/dfm/device-gateway/internal/fleetiger"
	"github.com/dfm/device-gateway/internal/howen"
	"github.com/dfm/device-gateway/internal/jt808"
	"github.com/dfm/device-gateway/internal/navtelecom"
)

func main() {
	app.Run(
		howen.New(),
		fleetiger.New(),
		cathexis.New(),
		navtelecom.New(),
		jt808.New(),
	)
}

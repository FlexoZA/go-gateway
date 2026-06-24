// Command howen runs the Howen unit-type gateway server.
//
// One binary, one unit type. All the gateway wiring lives in internal/core/app;
// this main just selects the Howen protocol. To create a server for a different
// unit type, scaffold cmd/<unit>/main.go with scripts/new-gateway.sh and swap in
// that protocol's New().
package main

import (
	"github.com/dfm/device-gateway/internal/core/app"
	"github.com/dfm/device-gateway/internal/howen"
)

func main() { app.Run(howen.New()) }

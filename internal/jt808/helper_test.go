package jt808

// Shared test fixtures. Event/mapping resolution now hangs off a *Protocol
// instance (per-unit mapping state), so tests use one shared unit — the N62 preset
// (unit "dfm-n62", model "N62").
var testProto = New(N62())

const deviceModel = "N62"

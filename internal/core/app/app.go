// Package app is the gateway composition root. It wires every unit-agnostic
// dependency (config, logger, hub, message builder, Postgres registry, telemetry
// webhook, media/clips, HTTP API) around a single gateway.Protocol and runs the
// startup sequence. One App serves exactly one Protocol — a unit-type binary is
// just `func main() { app.Run(myunit.New()) }`.
//
// Unit-specific wiring is reached only through optional interfaces the Protocol
// may implement (gateway.MappingProvider for editable event mappings/workflows,
// gateway.MediaServerProvider for a device-side media listener); a unit that
// implements neither — e.g. a plain GPS tracker — gets none of that machinery.
package app

import (
	"context"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/dfm/device-gateway/internal/core/config"
	"github.com/dfm/device-gateway/internal/core/device"
	"github.com/dfm/device-gateway/internal/core/eventcodes"
	"github.com/dfm/device-gateway/internal/core/gateway"
	"github.com/dfm/device-gateway/internal/core/httpapi"
	"github.com/dfm/device-gateway/internal/core/logging"
	"github.com/dfm/device-gateway/internal/core/media"
	"github.com/dfm/device-gateway/internal/core/message"
	"github.com/dfm/device-gateway/internal/core/postgres"
	"github.com/dfm/device-gateway/internal/core/webhook"
)

// App holds the composed gateway and every long-lived dependency.
type App struct {
	proto    gateway.Protocol
	cfg      config.Config
	log      *logging.Logger
	hub      *gateway.Hub
	builder  *message.Builder
	deps     gateway.Deps
	authMode string

	store       *postgres.Store         // may be nil (no database)
	webhookSink *webhook.Sink           // always wired (no-ops while empty)
	mediaMgr    *media.Manager          // nil when video disabled
	clipReg     *media.ClipRegistry     // nil when video/clips disabled
	mp          gateway.MappingProvider // non-nil when the unit has editable mappings
}

// Run builds and runs an App for proto, exiting non-zero on fatal error. This is
// the entire body of a unit-type binary's main().
func Run(proto gateway.Protocol) {
	if err := New(proto).Run(); err != nil {
		os.Exit(1)
	}
}

// New composes an App for proto: loads config, opens the database (if any),
// resolves the device port, wires the telemetry sink, and sets up video when
// enabled. It does the same fatal-exit-on-database-failure as the old main.
func New(proto gateway.Protocol) *App {
	cfg := config.Load()
	log := logging.New(proto.Name())

	a := &App{
		proto:    proto,
		cfg:      cfg,
		log:      log,
		hub:      gateway.NewHub(),
		authMode: "allow_all",
	}
	if mp, ok := proto.(gateway.MappingProvider); ok {
		a.mp = mp
	}

	// The message builder's gateway identifier is editable from server settings,
	// so keep a reference to update it live below.
	a.builder = message.NewBuilder(cfg.Gateway, cfg.WebhookTimezoneOffsetHours)
	a.deps = gateway.Deps{
		Config:  cfg,
		Log:     log,
		Builder: a.builder,
		Auth:    device.AllowAll{},
		Hub:     a.hub,
	}

	// PostgreSQL is the gateway's own DB (device verification registry), NOT a
	// telemetry store — GPS/event data goes to the webhook below.
	if cfg.DatabaseURL != "" {
		connCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		s, err := postgres.New(connCtx, cfg.DatabaseURL, cfg.DeviceRejectUnknown)
		cancel()
		if err != nil {
			log.Error(map[string]any{"event": "postgres_unavailable", "error": err.Error()})
			os.Exit(1)
		}
		a.store = s
		a.deps.DeviceErrors = s
		log.Info(map[string]any{"event": "database", "backend": "postgres", "purpose": "device_registry"})
		if cfg.DeviceAuthMode == "postgres" {
			a.deps.Auth = s
			a.authMode = "postgres"
			log.Info(map[string]any{"event": "device_auth", "mode": "postgres", "reject_unknown": cfg.DeviceRejectUnknown})
		}
	} else {
		log.Info(map[string]any{"event": "no_database", "detail": "DATABASE_URL not set; device auth = allow_all"})
	}

	// Device TCP port: a stored server setting can override LISTEN_PORT, applied on
	// (re)start only — a bound listener cannot change live, and in Docker the
	// container's published port must be updated to match. Resolve before binding.
	if a.store != nil {
		a.cfg.ListenPort = a.resolveDevicePort(a.cfg.ListenPort)
		a.deps.Config.ListenPort = a.cfg.ListenPort
	}

	// The webhook is the telemetry sink — it stores all GPS/event data. Its URL is
	// editable from the admin panel's server settings, so always wire the sink
	// (it no-ops while empty) and start from the env value; the stored value is
	// applied below once Postgres is available.
	a.webhookSink = webhook.New(cfg.WebhookURL)
	a.deps.Sinks = append(a.deps.Sinks, a.webhookSink)
	if a.webhookSink.Enabled() {
		log.Info(map[string]any{"event": "telemetry_sink", "backend": "webhook"})
	} else {
		log.Info(map[string]any{"event": "telemetry_sink_pending", "detail": "no webhook URL yet; set it in Server Settings or DEVICE_WEBHOOK_URL"})
	}

	// Live video (HLS): wire the media manager + the host the device dials back
	// for media frames. Disabled unless MEDIA_ADVERTISE_HOST is set.
	if cfg.VideoEnabled() {
		a.mediaMgr = media.NewManager(cfg.HLSRoot, cfg.FFmpegPath, log)
		a.deps.Media = a.mediaMgr
		a.deps.MediaAdvertiseHost = net.JoinHostPort(cfg.MediaAdvertiseHost, strconv.Itoa(cfg.MediaPort))
		a.deps.DeviceTZOffsetHours = cfg.DeviceTZOffsetHours
		// Recorded clips need the DB to track metadata; only enable when present.
		if a.store != nil {
			a.clipReg = media.NewClipRegistry(a.mediaMgr, a.store, cfg.ClipsRoot, log)
			a.deps.Clips = a.clipReg
			log.Info(map[string]any{"event": "clips_enabled", "clips_root": cfg.ClipsRoot})
		} else {
			log.Info(map[string]any{"event": "clips_disabled", "detail": "no database"})
		}
		log.Info(map[string]any{"event": "video_enabled", "advertise": a.deps.MediaAdvertiseHost, "hls_root": cfg.HLSRoot})
	} else {
		log.Info(map[string]any{"event": "video_disabled", "detail": "MEDIA_ADVERTISE_HOST not set"})
	}

	return a
}

// Run starts the server and blocks until the process is signalled. It returns a
// non-nil error only on a fatal listen failure.
func (a *App) Run() error {
	if a.store != nil {
		defer a.store.Close()
	}

	srv := gateway.New(a.proto, a.deps)
	// A unit whose devices speak infrequently can widen the per-connection idle
	// timeout beyond the framework default.
	if it, ok := a.proto.(gateway.IdleTimeoutProvider); ok {
		srv.SetIdleTimeout(it.IdleTimeout())
		a.log.Info(map[string]any{"event": "idle_timeout_override", "timeout": it.IdleTimeout().String()})
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Persist system/server/gateway errors: tee every Error-level log line into the
	// gateway_errors table (async, drop-on-full so logging never blocks on the DB).
	if a.store != nil {
		a.log.SetErrorSink(a.store.StartErrorLogSink(ctx, a.proto.Name(), a.log))
	}

	if a.store != nil {
		a.startStoreBackedServices(ctx)
	}

	// Management/control HTTP API (API-key protected).
	if a.cfg.HTTPPort > 0 {
		a.startHTTPAPI(ctx)
	}

	// Media server: accepts the device's video connections (started by a stream
	// command). Only runs when video is enabled and the unit provides a listener.
	if a.mediaMgr != nil {
		if msp, ok := a.proto.(gateway.MediaServerProvider); ok {
			ms := msp.NewMediaServer(
				net.JoinHostPort(a.cfg.ListenHost, strconv.Itoa(a.cfg.MediaPort)),
				a.mediaMgr, a.clipReg, a.log)
			go func() {
				if err := ms.ListenAndServe(ctx); err != nil {
					a.log.Error(map[string]any{"event": "media_fatal", "error": err.Error()})
				}
			}()
		}
	}

	a.log.Info(map[string]any{"event": "starting", "unit": a.proto.Name(), "device_auth_mode": a.authMode})
	if err := srv.ListenAndServe(ctx); err != nil {
		a.log.Error(map[string]any{"event": "fatal", "error": err.Error()})
		return err
	}
	a.log.Info(map[string]any{"event": "stopped"})
	return nil
}

// startStoreBackedServices wires everything that needs the database: editable
// mappings/workflows (with instant LISTEN/NOTIFY reload), the event-code picklist,
// telemetry webhooks, and live server settings.
func (a *App) startStoreBackedServices(ctx context.Context) {
	// Front-end-editable event mappings + per-model workflows. Only when the unit
	// drives its output from them (MappingProvider) — a plain GPS unit skips all
	// of this. Edits apply instantly via LISTEN/NOTIFY; a periodic reload is a
	// safety net in case a notification is ever missed.
	if a.mp != nil {
		a.seedAndLoadMappings(ctx)
		a.seedEventCodes(ctx)
		go a.store.ListenForMappingChanges(ctx, func(string) { a.reloadMappings(ctx) })
		if a.cfg.MappingRefreshSeconds > 0 {
			go a.refreshMappings(ctx, time.Duration(a.cfg.MappingRefreshSeconds)*time.Second)
		}

		a.loadWorkflows(ctx)
		go a.store.ListenForWorkflowChanges(ctx, func(string) { a.loadWorkflows(ctx) })
	}

	// Telemetry webhooks: migrate the original single URL (env / legacy webhook_url
	// setting) into the webhooks table on first run, load the enabled set into the
	// sink, and reload instantly on any change.
	a.seedWebhooks(ctx)
	a.applyWebhooks(ctx)
	go a.store.ListenForWebhookChanges(ctx, func(string) { a.applyWebhooks(ctx) })

	// Record the port we actually bound so the panel can flag a pending restart
	// when the configured device_port differs from the running one.
	if err := a.store.SetSetting(ctx, postgres.SettingDevicePortActive, strconv.Itoa(a.cfg.ListenPort)); err != nil {
		a.log.Debug(map[string]any{"event": "device_port_active_write_failed", "error": err.Error()})
	}

	// Live server settings: seed defaults from env on first run, apply the stored
	// values, and reload instantly on any change (gateway name + device-auth gate).
	if err := a.store.SeedSettingDefault(ctx, postgres.SettingGatewayName, a.cfg.Gateway); err != nil {
		a.log.Error(map[string]any{"event": "gateway_name_seed_failed", "error": err.Error()})
	}
	if err := a.store.SeedSettingDefault(ctx, postgres.SettingDeviceRejectUnknown, strconv.FormatBool(a.cfg.DeviceRejectUnknown)); err != nil {
		a.log.Error(map[string]any{"event": "reject_unknown_seed_failed", "error": err.Error()})
	}
	applyLiveSettings := func() {
		a.applyGatewayName(ctx)
		a.applyRejectUnknown(ctx)
	}
	applyLiveSettings()
	go a.store.ListenForSettingsChanges(ctx, func(string) { applyLiveSettings() })
}

// startHTTPAPI builds and runs the management/control HTTP API.
func (a *App) startHTTPAPI(ctx context.Context) {
	var verifier httpapi.KeyVerifier
	var data httpapi.DataStore
	if a.store != nil {
		verifier = a.store
		data = a.store
	}
	api := httpapi.New(a.cfg.ListenHost, a.cfg.HTTPPort, a.proto.Name(), verifier, data, a.hub, a.log)
	api.SetInternalToken(a.cfg.InternalAPIToken)
	if a.cfg.InternalAPIToken != "" {
		a.log.Info(map[string]any{"event": "internal_token_enabled"})
	}
	if a.mediaMgr != nil {
		api.SetHLSRoot(a.cfg.HLSRoot)
	}
	if a.clipReg != nil {
		api.SetClipsRoot(a.cfg.ClipsRoot)
	}
	api.SetCapabilities(a.effectiveCapabilities())
	go func() {
		if err := api.Run(ctx); err != nil {
			a.log.Error(map[string]any{"event": "http_fatal", "error": err.Error()})
		}
	}()
}

// effectiveCapabilities is what this running gateway actually offers: the unit's
// declared capabilities gated by runtime configuration.
func (a *App) effectiveCapabilities() gateway.EffectiveCapabilities {
	caps := a.proto.Capabilities()
	return gateway.EffectiveCapabilities{
		HasVideo:    caps.HasVideo && a.cfg.VideoEnabled(),
		HasCommands: caps.HasCommands,
		HasConfig:   caps.HasConfig,
		HasStatus:   caps.HasStatus,
		HasClips:    a.mediaMgr != nil && a.clipReg != nil,
		HasMappings: a.mp != nil,
	}
}

// seedAndLoadMappings seeds the unit's built-in mapping defaults into the database
// (no-op for rows that already exist) and applies the current set to the live maps.
func (a *App) seedAndLoadMappings(ctx context.Context) {
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := a.store.SeedEventMappings(cctx, a.proto.Name(), a.mp.DefaultMappingEntries()); err != nil {
		a.log.Error(map[string]any{"event": "mapping_seed_failed", "error": err.Error()})
	}
	loaded, err := a.store.LoadEventMappings(cctx, a.proto.Name())
	if err != nil {
		a.log.Error(map[string]any{"event": "mapping_load_failed", "error": err.Error(), "detail": "using built-in defaults"})
		return
	}
	a.mp.ApplyMappings(loaded)
	a.log.Info(map[string]any{"event": "mappings_loaded", "map_types": len(loaded)})
}

// seedEventCodes loads the embedded ACM Standard Event Codes into the database so
// the front end can offer them as a picklist. Idempotent (upsert).
func (a *App) seedEventCodes(ctx context.Context) {
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	codes := eventcodes.Standard()
	if err := a.store.SeedStandardEventCodes(cctx, codes); err != nil {
		a.log.Error(map[string]any{"event": "event_codes_seed_failed", "error": err.Error()})
		return
	}
	a.log.Info(map[string]any{"event": "event_codes_seeded", "count": len(codes)})
}

// reloadMappings loads the current mappings and applies them to the live maps.
// Used by both the LISTEN/NOTIFY handler and the periodic safety-net.
func (a *App) reloadMappings(ctx context.Context) {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	loaded, err := a.store.LoadEventMappings(cctx, a.proto.Name())
	if err != nil {
		a.log.Debug(map[string]any{"event": "mapping_reload_failed", "error": err.Error()})
		return
	}
	a.mp.ApplyMappings(loaded)
	a.log.Debug(map[string]any{"event": "mappings_reloaded", "map_types": len(loaded)})
}

// loadWorkflows loads the active per-model mapping workflows and installs them.
// Called at startup and on every LISTEN/NOTIFY change so edits apply instantly.
func (a *App) loadWorkflows(ctx context.Context) {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	wf, err := a.store.LoadActiveWorkflows(cctx, a.proto.Name())
	if err != nil {
		a.log.Debug(map[string]any{"event": "workflow_load_failed", "error": err.Error()})
		return
	}
	a.mp.ApplyWorkflows(wf)
	a.log.Info(map[string]any{"event": "workflows_loaded", "models": a.mp.WorkflowModelCount()})
}

// refreshMappings periodically reloads mappings as a safety net behind
// LISTEN/NOTIFY (covers a missed notification on a dropped connection).
func (a *App) refreshMappings(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.reloadMappings(ctx)
		}
	}
}

// resolveDevicePort returns the device TCP port to bind: the stored device_port
// server setting when set and valid, else the env/default port. The setting is
// seeded from the env value on first run. Applied at startup only.
func (a *App) resolveDevicePort(envPort int) int {
	cctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = a.store.SeedSettingDefault(cctx, postgres.SettingDevicePort, strconv.Itoa(envPort))
	v, ok, err := a.store.GetSetting(cctx, postgres.SettingDevicePort)
	if err != nil || !ok {
		return envPort
	}
	p, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || p < 1 || p > 65535 {
		a.log.Error(map[string]any{"event": "device_port_invalid", "value": v, "fallback": envPort})
		return envPort
	}
	if p != envPort {
		a.log.Info(map[string]any{"event": "device_port_override", "configured": p, "env": envPort})
	}
	return p
}

// applyGatewayName loads the stored gateway identifier and installs it on the
// message builder so every universal message carries it. Called at startup and on
// every settings change.
func (a *App) applyGatewayName(ctx context.Context) {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	v, ok, err := a.store.GetSetting(cctx, postgres.SettingGatewayName)
	if err != nil {
		a.log.Debug(map[string]any{"event": "gateway_name_apply_failed", "error": err.Error()})
		return
	}
	if ok {
		a.builder.SetGateway(v)
		a.log.Info(map[string]any{"event": "gateway_name_applied", "gateway": v})
	}
}

// applyRejectUnknown loads the device-authorization gate and installs it on the
// store so it takes effect for subsequent device connections. Called at startup
// and on every settings change.
func (a *App) applyRejectUnknown(ctx context.Context) {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	v, ok, err := a.store.GetSetting(cctx, postgres.SettingDeviceRejectUnknown)
	if err != nil {
		a.log.Debug(map[string]any{"event": "reject_unknown_apply_failed", "error": err.Error()})
		return
	}
	if ok {
		reject := parseBoolSetting(v)
		a.store.SetRejectUnknown(reject)
		a.log.Info(map[string]any{"event": "device_reject_unknown_applied", "reject": reject})
	}
}

// seedWebhooks migrates the original single webhook URL into the webhooks table on
// first run. It prefers a value previously set via the (now legacy) webhook_url
// server setting, falling back to DEVICE_WEBHOOK_URL. No-op once any webhook exists.
func (a *App) seedWebhooks(ctx context.Context) {
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	url := a.cfg.WebhookURL
	if v, ok, err := a.store.GetSetting(cctx, postgres.SettingWebhookURL); err == nil && ok && v != "" {
		url = v
	}
	seeded, err := a.store.SeedWebhookIfEmpty(cctx, "default", url)
	if err != nil {
		a.log.Error(map[string]any{"event": "webhook_seed_failed", "error": err.Error()})
		return
	}
	if seeded {
		a.log.Info(map[string]any{"event": "webhook_seeded", "url": url})
	}
}

// applyWebhooks loads the enabled webhook URLs and installs them as the sink's
// targets. Called at startup and on every webhooks change.
func (a *App) applyWebhooks(ctx context.Context) {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	urls, err := a.store.LoadEnabledWebhookURLs(cctx)
	if err != nil {
		a.log.Debug(map[string]any{"event": "webhooks_apply_failed", "error": err.Error()})
		return
	}
	a.webhookSink.SetTargets(urls)
	a.log.Info(map[string]any{"event": "webhooks_applied", "count": len(urls)})
}

// parseBoolSetting parses a stored boolean setting value.
func parseBoolSetting(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

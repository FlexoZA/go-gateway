// Command howen runs the Howen unit-type gateway server.
//
// One binary, one unit type. To create a server for a different unit type, copy
// this file to cmd/<unit>/main.go and swap howen.New() for that protocol plugin.
package main

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
	"github.com/dfm/device-gateway/internal/howen"
)

func main() {
	cfg := config.Load()
	log := logging.New("howen")

	hub := gateway.NewHub()
	// The message builder's gateway identifier is editable from server settings,
	// so keep a reference to update it live below.
	builder := message.NewBuilder(cfg.Gateway, cfg.WebhookTimezoneOffsetHours)
	deps := gateway.Deps{
		Config:  cfg,
		Log:     log,
		Builder: builder,
		Auth:    device.AllowAll{},
		Hub:     hub,
	}
	authMode := "allow_all"

	// PostgreSQL is the gateway's own DB (device verification registry), NOT a
	// telemetry store — GPS/event data goes to the webhook below.
	var store *postgres.Store
	if cfg.DatabaseURL != "" {
		connCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		s, err := postgres.New(connCtx, cfg.DatabaseURL, cfg.DeviceRejectUnknown)
		cancel()
		if err != nil {
			log.Error(map[string]any{"event": "postgres_unavailable", "error": err.Error()})
			os.Exit(1)
		}
		store = s
		defer store.Close()
		deps.DeviceErrors = store
		log.Info(map[string]any{"event": "database", "backend": "postgres", "purpose": "device_registry"})
		if cfg.DeviceAuthMode == "postgres" {
			deps.Auth = store
			authMode = "postgres"
			log.Info(map[string]any{"event": "device_auth", "mode": "postgres", "reject_unknown": cfg.DeviceRejectUnknown})
		}
	} else {
		log.Info(map[string]any{"event": "no_database", "detail": "DATABASE_URL not set; device auth = allow_all"})
	}

	// Device TCP port: a stored server setting can override LISTEN_PORT, applied on
	// (re)start only — a bound listener cannot change live, and in Docker the
	// container's published port must be updated to match. Resolve before binding.
	if store != nil {
		cfg.ListenPort = resolveDevicePort(store, cfg.ListenPort, log)
		deps.Config.ListenPort = cfg.ListenPort
	}

	// The webhook is the telemetry sink — it stores all GPS/event data. Its URL is
	// editable from the admin panel's server settings, so always wire the sink
	// (it no-ops while empty) and start from the env value; the stored value is
	// applied below once Postgres is available.
	webhookSink := webhook.New(cfg.WebhookURL)
	deps.Sinks = append(deps.Sinks, webhookSink)
	if webhookSink.Enabled() {
		log.Info(map[string]any{"event": "telemetry_sink", "backend": "webhook"})
	} else {
		log.Info(map[string]any{"event": "telemetry_sink_pending", "detail": "no webhook URL yet; set it in Server Settings or DEVICE_WEBHOOK_URL"})
	}

	// Live video (HLS): wire the media manager + the host the device dials back
	// for media frames. Disabled unless MEDIA_ADVERTISE_HOST is set.
	var mediaMgr *media.Manager
	if cfg.VideoEnabled() {
		mediaMgr = media.NewManager(cfg.HLSRoot, cfg.FFmpegPath, log)
		deps.Media = mediaMgr
		deps.MediaAdvertiseHost = net.JoinHostPort(cfg.MediaAdvertiseHost, strconv.Itoa(cfg.MediaPort))
		log.Info(map[string]any{"event": "video_enabled", "advertise": deps.MediaAdvertiseHost, "hls_root": cfg.HLSRoot})
	} else {
		log.Info(map[string]any{"event": "video_disabled", "detail": "MEDIA_ADVERTISE_HOST not set"})
	}

	proto := howen.New()
	srv := gateway.New(proto, deps)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Persist system/server/gateway errors: tee every Error-level log line into the
	// gateway_errors table (async, drop-on-full so logging never blocks on the DB).
	if store != nil {
		log.SetErrorSink(store.StartErrorLogSink(ctx, "howen", log))
	}

	// Front-end-editable event mappings: seed defaults and load the current set.
	// Edits apply instantly via LISTEN/NOTIFY; a periodic reload is a safety net
	// in case a notification is ever missed. Built-in maps are the fallback.
	if store != nil {
		seedAndLoadMappings(ctx, store, log)
		// Seed the canonical event-code picklist the admin panel offers.
		seedEventCodes(ctx, store, log)
		// Instant updates: reload the moment a mapping row changes.
		go store.ListenForMappingChanges(ctx, func(string) { reloadMappings(ctx, store, log) })
		// Safety-net resync.
		if cfg.MappingRefreshSeconds > 0 {
			go refreshMappings(ctx, store, time.Duration(cfg.MappingRefreshSeconds)*time.Second, log)
		}

		// Per-model visual mapping workflows: load the active set and reload
		// instantly when one changes (same LISTEN/NOTIFY mechanism as mappings).
		loadWorkflows(ctx, store, log)
		go store.ListenForWorkflowChanges(ctx, func(string) { loadWorkflows(ctx, store, log) })

		// Telemetry webhooks: migrate the original single URL (env / legacy
		// webhook_url setting) into the webhooks table on first run, load the
		// enabled set into the sink, and reload instantly on any change.
		seedWebhooks(ctx, store, cfg, log)
		applyWebhooks(ctx, store, webhookSink, log)
		go store.ListenForWebhookChanges(ctx, func(string) { applyWebhooks(ctx, store, webhookSink, log) })

		// Record the port we actually bound so the panel can flag a pending restart
		// when the configured device_port differs from the running one.
		if err := store.SetSetting(ctx, postgres.SettingDevicePortActive, strconv.Itoa(cfg.ListenPort)); err != nil {
			log.Debug(map[string]any{"event": "device_port_active_write_failed", "error": err.Error()})
		}

		// Live server settings: seed defaults from env on first run, apply the
		// stored values, and reload instantly on any change. Currently the gateway
		// name (embedded in every message) and the device-authorization gate.
		if err := store.SeedSettingDefault(ctx, postgres.SettingGatewayName, cfg.Gateway); err != nil {
			log.Error(map[string]any{"event": "gateway_name_seed_failed", "error": err.Error()})
		}
		if err := store.SeedSettingDefault(ctx, postgres.SettingDeviceRejectUnknown, strconv.FormatBool(cfg.DeviceRejectUnknown)); err != nil {
			log.Error(map[string]any{"event": "reject_unknown_seed_failed", "error": err.Error()})
		}
		applyLiveSettings := func() {
			applyGatewayName(ctx, store, builder, log)
			applyRejectUnknown(ctx, store, log)
		}
		applyLiveSettings()
		go store.ListenForSettingsChanges(ctx, func(string) { applyLiveSettings() })
	}

	// Management/control HTTP API (API-key protected). Endpoints land in
	// milestone 2; the key check is already enforced.
	if cfg.HTTPPort > 0 {
		var verifier httpapi.KeyVerifier
		var data httpapi.DataStore
		if store != nil {
			verifier = store
			data = store
		}
		api := httpapi.New(cfg.ListenHost, cfg.HTTPPort, proto.Name(), verifier, data, hub, log)
		api.SetInternalToken(cfg.InternalAPIToken)
		if cfg.InternalAPIToken != "" {
			log.Info(map[string]any{"event": "internal_token_enabled"})
		}
		if mediaMgr != nil {
			api.SetHLSRoot(cfg.HLSRoot)
		}
		go func() {
			if err := api.Run(ctx); err != nil {
				log.Error(map[string]any{"event": "http_fatal", "error": err.Error()})
			}
		}()
	}

	// Media server: accepts the device's video connections (started by a
	// stream command). Only runs when video is enabled.
	if mediaMgr != nil {
		ms := &howen.MediaServer{
			Addr:    net.JoinHostPort(cfg.ListenHost, strconv.Itoa(cfg.MediaPort)),
			Manager: mediaMgr,
			Log:     log,
		}
		go func() {
			if err := ms.ListenAndServe(ctx); err != nil {
				log.Error(map[string]any{"event": "media_fatal", "error": err.Error()})
			}
		}()
	}

	log.Info(map[string]any{"event": "starting", "unit": "howen", "device_auth_mode": authMode})
	if err := srv.ListenAndServe(ctx); err != nil {
		log.Error(map[string]any{"event": "fatal", "error": err.Error()})
		os.Exit(1)
	}
	log.Info(map[string]any{"event": "stopped"})
}

// seedAndLoadMappings seeds the built-in Howen defaults into the database (no-op
// for rows that already exist) and applies the current set to the live maps.
func seedAndLoadMappings(ctx context.Context, store *postgres.Store, log *logging.Logger) {
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := store.SeedEventMappings(cctx, "howen", howen.DefaultMappingEntries()); err != nil {
		log.Error(map[string]any{"event": "mapping_seed_failed", "error": err.Error()})
	}
	loaded, err := store.LoadEventMappings(cctx, "howen")
	if err != nil {
		log.Error(map[string]any{"event": "mapping_load_failed", "error": err.Error(), "detail": "using built-in defaults"})
		return
	}
	howen.ApplyMappings(loaded)
	log.Info(map[string]any{"event": "mappings_loaded", "map_types": len(loaded)})
}

// seedEventCodes loads the embedded ACM Standard Event Codes into the database
// so the front end can offer them as a picklist. Idempotent (upsert).
func seedEventCodes(ctx context.Context, store *postgres.Store, log *logging.Logger) {
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	codes := eventcodes.Standard()
	if err := store.SeedStandardEventCodes(cctx, codes); err != nil {
		log.Error(map[string]any{"event": "event_codes_seed_failed", "error": err.Error()})
		return
	}
	log.Info(map[string]any{"event": "event_codes_seeded", "count": len(codes)})
}

// resolveDevicePort returns the device TCP port to bind: the stored device_port
// server setting when set and valid, else the env/default port. The setting is
// seeded from the env value on first run. Applied at startup only.
func resolveDevicePort(store *postgres.Store, envPort int, log *logging.Logger) int {
	cctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = store.SeedSettingDefault(cctx, postgres.SettingDevicePort, strconv.Itoa(envPort))
	v, ok, err := store.GetSetting(cctx, postgres.SettingDevicePort)
	if err != nil || !ok {
		return envPort
	}
	p, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || p < 1 || p > 65535 {
		log.Error(map[string]any{"event": "device_port_invalid", "value": v, "fallback": envPort})
		return envPort
	}
	if p != envPort {
		log.Info(map[string]any{"event": "device_port_override", "configured": p, "env": envPort})
	}
	return p
}

// applyGatewayName loads the stored gateway identifier and installs it on the
// message builder so every universal message carries it. Called at startup and
// on every settings change.
func applyGatewayName(ctx context.Context, store *postgres.Store, builder *message.Builder, log *logging.Logger) {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	v, ok, err := store.GetSetting(cctx, postgres.SettingGatewayName)
	if err != nil {
		log.Debug(map[string]any{"event": "gateway_name_apply_failed", "error": err.Error()})
		return
	}
	if ok {
		builder.SetGateway(v)
		log.Info(map[string]any{"event": "gateway_name_applied", "gateway": v})
	}
}

// applyRejectUnknown loads the device-authorization gate and installs it on the
// store so it takes effect for subsequent device connections. Called at startup
// and on every settings change.
func applyRejectUnknown(ctx context.Context, store *postgres.Store, log *logging.Logger) {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	v, ok, err := store.GetSetting(cctx, postgres.SettingDeviceRejectUnknown)
	if err != nil {
		log.Debug(map[string]any{"event": "reject_unknown_apply_failed", "error": err.Error()})
		return
	}
	if ok {
		reject := parseBoolSetting(v)
		store.SetRejectUnknown(reject)
		log.Info(map[string]any{"event": "device_reject_unknown_applied", "reject": reject})
	}
}

// parseBoolSetting parses a stored boolean setting value.
func parseBoolSetting(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// seedWebhooks migrates the original single webhook URL into the webhooks table
// on first run. It prefers a value previously set via the (now legacy)
// webhook_url server setting, falling back to DEVICE_WEBHOOK_URL. No-op once any
// webhook exists.
func seedWebhooks(ctx context.Context, store *postgres.Store, cfg config.Config, log *logging.Logger) {
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	url := cfg.WebhookURL
	if v, ok, err := store.GetSetting(cctx, postgres.SettingWebhookURL); err == nil && ok && v != "" {
		url = v
	}
	seeded, err := store.SeedWebhookIfEmpty(cctx, "default", url)
	if err != nil {
		log.Error(map[string]any{"event": "webhook_seed_failed", "error": err.Error()})
		return
	}
	if seeded {
		log.Info(map[string]any{"event": "webhook_seeded", "url": url})
	}
}

// applyWebhooks loads the enabled webhook URLs and installs them as the sink's
// targets. Called at startup and on every webhooks change.
func applyWebhooks(ctx context.Context, store *postgres.Store, sink *webhook.Sink, log *logging.Logger) {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	urls, err := store.LoadEnabledWebhookURLs(cctx)
	if err != nil {
		log.Debug(map[string]any{"event": "webhooks_apply_failed", "error": err.Error()})
		return
	}
	sink.SetTargets(urls)
	log.Info(map[string]any{"event": "webhooks_applied", "count": len(urls)})
}

// reloadMappings loads the current mappings and applies them to the live maps.
// Used by both the LISTEN/NOTIFY handler and the periodic safety-net.
func reloadMappings(ctx context.Context, store *postgres.Store, log *logging.Logger) {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	loaded, err := store.LoadEventMappings(cctx, "howen")
	if err != nil {
		log.Debug(map[string]any{"event": "mapping_reload_failed", "error": err.Error()})
		return
	}
	howen.ApplyMappings(loaded)
	log.Debug(map[string]any{"event": "mappings_reloaded", "map_types": len(loaded)})
}

// loadWorkflows loads the active per-model mapping workflows and installs them.
// Called at startup and on every LISTEN/NOTIFY change so edits apply instantly.
func loadWorkflows(ctx context.Context, store *postgres.Store, log *logging.Logger) {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	wf, err := store.LoadActiveWorkflows(cctx, "howen")
	if err != nil {
		log.Debug(map[string]any{"event": "workflow_load_failed", "error": err.Error()})
		return
	}
	howen.ApplyWorkflows(wf)
	log.Info(map[string]any{"event": "workflows_loaded", "models": howen.WorkflowModelCount()})
}

// refreshMappings periodically reloads mappings as a safety net behind
// LISTEN/NOTIFY (covers a missed notification on a dropped connection).
func refreshMappings(ctx context.Context, store *postgres.Store, interval time.Duration, log *logging.Logger) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			reloadMappings(ctx, store, log)
		}
	}
}

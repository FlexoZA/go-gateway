// Package app is the gateway composition root. It wires every unit-agnostic
// dependency (config, logger, hub, message builder, Postgres registry, telemetry
// webhook, media/clips, HTTP API) around one OR MORE gateway.Protocol plugins and
// runs the startup sequence. One process hosts every registered unit type, each on
// its own TCP listener/port, sharing all the infrastructure above — a unit-type
// binary is just `func main() { app.Run(unitA.New(), unitB.New()) }`.
//
// Unit-specific wiring is reached only through optional interfaces a Protocol may
// implement (gateway.DefaultPort for its port, gateway.MappingProvider for editable
// event mappings, gateway.ConfigurableUnit for unit-type settings,
// gateway.MediaServerProvider for a device-side media listener, gateway.
// IdleTimeoutProvider for a custom read deadline); a unit that implements none —
// e.g. a plain GPS tracker — gets none of that machinery.
package app

import (
	"context"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
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

// unitRuntime is one hosted unit type: its protocol plugin plus the per-unit state
// the runner wires around it. deps is a clone of the shared base deps with this
// unit's listen port (and, for a video unit, its media manager/clips) set.
type unitRuntime struct {
	proto     gateway.Protocol
	name      string
	port      int
	mediaPort int // device-side media port (video units only)
	deps      gateway.Deps
	caps      gateway.EffectiveCapabilities
	mp        gateway.MappingProvider  // nil unless the unit has editable mappings
	cfgUnit   gateway.ConfigurableUnit // nil unless the unit declares settings
	settings  *gateway.UnitSettings    // non-nil iff cfgUnit != nil
	media     *media.Manager           // nil unless a video unit with video enabled
	clips     *media.ClipRegistry      // nil unless a video unit with a database
}

// App holds the composed gateway and every long-lived dependency.
type App struct {
	cfg      config.Config
	log      *logging.Logger
	hub      *gateway.Hub
	builder  *message.Builder
	authMode string

	store       *postgres.Store // may be nil (no database)
	webhookSink *webhook.Sink   // always wired (no-ops while empty)

	units []*unitRuntime
}

// Run builds and runs an App for the given unit-type protocols, exiting non-zero
// on fatal error. This is the entire body of a gateway binary's main().
func Run(protos ...gateway.Protocol) {
	if err := New(protos...).Run(); err != nil {
		os.Exit(1)
	}
}

// New composes an App: loads config, opens the database (if any), wires the shared
// telemetry sink, and builds a per-unit runtime (resolved port, cloned deps, media
// when enabled, settings holder) for each protocol. It fatal-exits on a database
// failure, like the old single-unit main.
func New(protos ...gateway.Protocol) *App {
	cfg := config.Load()
	log := logging.New("gateway")

	a := &App{
		cfg:      cfg,
		log:      log,
		hub:      gateway.NewHub(),
		authMode: "allow_all",
	}

	// The message builder's gateway identifier is editable from server settings,
	// so keep a reference to update it live below.
	a.builder = message.NewBuilder(cfg.Gateway, cfg.WebhookTimezoneOffsetHours)

	// Shared base dependencies. Each unit gets a value copy with its own port (and
	// media/settings) below; Builder/Sinks/Auth/Hub are shared pointers/slices.
	baseDeps := gateway.Deps{
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
		baseDeps.DeviceErrors = s
		log.Info(map[string]any{"event": "database", "backend": "postgres", "purpose": "device_registry"})
		if cfg.DeviceAuthMode == "postgres" {
			baseDeps.Auth = s
			a.authMode = "postgres"
			log.Info(map[string]any{"event": "device_auth", "mode": "postgres", "reject_unknown": cfg.DeviceRejectUnknown})
		}
	} else {
		log.Info(map[string]any{"event": "no_database", "detail": "DATABASE_URL not set; device auth = allow_all"})
	}

	// The webhook is the telemetry sink — it stores all GPS/event data. Its URL is
	// editable from the admin panel; always wire the sink (it no-ops while empty)
	// and start from the env value; the stored value is applied below.
	a.webhookSink = webhook.New(cfg.WebhookURL)
	baseDeps.Sinks = append(baseDeps.Sinks, a.webhookSink)
	if a.webhookSink.Enabled() {
		log.Info(map[string]any{"event": "telemetry_sink", "backend": "webhook"})
	} else {
		log.Info(map[string]any{"event": "telemetry_sink_pending", "detail": "no webhook URL yet; set it in Server Settings or DEVICE_WEBHOOK_URL"})
	}

	// Build a runtime per unit type. Each resolves its own admin-editable port.
	for _, proto := range protos {
		a.units = append(a.units, a.newUnitRuntime(proto, baseDeps))
	}

	return a
}

// newUnitRuntime resolves a unit's port, clones the base deps for it, and wires its
// optional features (video, settings).
func (a *App) newUnitRuntime(proto gateway.Protocol, baseDeps gateway.Deps) *unitRuntime {
	u := &unitRuntime{proto: proto, name: proto.Name()}
	u.port = a.resolveUnitPort(proto)

	// Clone deps for this unit and set its listen port (Conn.Emit reports it).
	u.deps = baseDeps
	u.deps.Config = a.cfg
	u.deps.Config.ListenPort = u.port

	// Live video (HLS): only for a unit that runs a device-side media listener AND
	// when video is enabled (a media advertise host is configured). Today only the
	// howen unit qualifies; a GPS-only unit's deps.Media stays nil.
	if _, ok := proto.(gateway.MediaServerProvider); ok && a.cfg.VideoEnabled() {
		// A unit may declare its own media-port default so two video units in one
		// process don't collide on the shared MEDIA_PORT; the stored override wins.
		mediaBase := a.cfg.MediaPort
		if dmp, ok := proto.(gateway.DefaultMediaPort); ok {
			mediaBase = dmp.DefaultMediaPort()
		}
		u.mediaPort = a.resolveStoredPort(httpapi.MediaPortKey(u.name), mediaBase)
		u.media = media.NewManager(a.cfg.HLSRoot, a.cfg.FFmpegPath, a.log)
		u.deps.Media = u.media
		u.deps.MediaAdvertiseHost = net.JoinHostPort(a.cfg.MediaAdvertiseHost, strconv.Itoa(u.mediaPort))
		u.deps.DeviceTZOffsetHours = a.cfg.DeviceTZOffsetHours
		if a.store != nil {
			u.clips = media.NewClipRegistry(u.media, a.store, a.cfg.ClipsRoot, a.log)
			u.deps.Clips = u.clips
			a.log.Info(map[string]any{"event": "clips_enabled", "unit": u.name, "clips_root": a.cfg.ClipsRoot})
		} else {
			a.log.Info(map[string]any{"event": "clips_disabled", "unit": u.name, "detail": "no database"})
		}
		a.log.Info(map[string]any{"event": "video_enabled", "unit": u.name, "advertise": u.deps.MediaAdvertiseHost})
	}

	// Editable code→event mappings.
	if mp, ok := proto.(gateway.MappingProvider); ok {
		u.mp = mp
	}

	// Per-unit-type settings: build the holder and seed it from the schema defaults
	// immediately so the unit works even with no database; stored values (if any)
	// are loaded over the top at startup.
	if cu, ok := proto.(gateway.ConfigurableUnit); ok {
		u.cfgUnit = cu
		u.settings = gateway.NewUnitSettings()
		defaults := map[string]string{}
		for _, f := range cu.SettingsSchema() {
			defaults[f.Key] = f.Default
		}
		u.settings.Replace(defaults)
		u.deps.UnitSettings = u.settings
	}

	u.caps = a.effectiveCapabilities(u)
	return u
}

// resolveUnitPort picks a unit's device TCP port: the admin-editable per-unit
// device_port setting (if present) → <UNITNAME>_PORT env → DefaultDevicePort() →
// the generic LISTEN_PORT. The stored setting is seeded from the env/default on
// first run, then becomes the source of truth (applied on restart).
func (a *App) resolveUnitPort(proto gateway.Protocol) int {
	base := a.cfg.ListenPort
	if dp, ok := proto.(gateway.DefaultPort); ok {
		base = dp.DefaultDevicePort()
	}
	if v := strings.TrimSpace(os.Getenv(strings.ToUpper(proto.Name()) + "_PORT")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 65535 {
			base = n
		} else {
			a.log.Error(map[string]any{"event": "unit_port_invalid", "unit": proto.Name(), "value": v, "fallback": base})
		}
	}
	return a.resolveStoredPort(httpapi.DevicePortKey(proto.Name()), base)
}

// resolveStoredPort seeds a port setting from base on first run and returns the
// stored value (admin-editable, applied on restart), falling back to base when
// there's no database or the stored value is invalid.
func (a *App) resolveStoredPort(key string, base int) int {
	if a.store == nil {
		return base
	}
	cctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = a.store.SeedSettingDefault(cctx, key, strconv.Itoa(base))
	v, ok, err := a.store.GetSetting(cctx, key)
	if err != nil || !ok {
		return base
	}
	p, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || p < 1 || p > 65535 {
		a.log.Error(map[string]any{"event": "port_setting_invalid", "key": key, "value": v, "fallback": base})
		return base
	}
	if p != base {
		a.log.Info(map[string]any{"event": "port_override", "key": key, "configured": p, "base": base})
	}
	return p
}

// Run starts every unit's listener and blocks until the process is signalled. It
// returns a non-nil error only on a fatal listen failure (e.g. a port already in
// use), which also shuts the other listeners down.
func (a *App) Run() error {
	if a.store != nil {
		defer a.store.Close()
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Persist system/server/gateway errors: tee every Error-level log line into the
	// gateway_errors table (async, drop-on-full so logging never blocks on the DB).
	if a.store != nil {
		a.log.SetErrorSink(a.store.StartErrorLogSink(ctx, "gateway", a.log))
		a.startStoreBackedServices(ctx)
	}

	// Management/control HTTP API (API-key protected).
	if a.cfg.HTTPPort > 0 {
		a.startHTTPAPI(ctx)
	}

	// Media servers: accept the device's video connections (one per video unit).
	for _, u := range a.units {
		if u.media == nil {
			continue
		}
		msp, ok := u.proto.(gateway.MediaServerProvider)
		if !ok {
			continue
		}
		ms := msp.NewMediaServer(net.JoinHostPort(a.cfg.ListenHost, strconv.Itoa(u.mediaPort)), u.media, u.clips, a.log)
		go func(ms gateway.MediaListener, name string) {
			if err := ms.ListenAndServe(ctx); err != nil {
				a.log.Error(map[string]any{"event": "media_fatal", "unit": name, "error": err.Error()})
			}
		}(ms, u.name)
		// Reap live streams abandoned by the device (media connection dropped) or
		// the viewer (browser left without stopping) so ffmpeg can't pile up.
		u.media.StartReaper(ctx)
	}

	a.log.Info(map[string]any{"event": "starting", "units": a.unitNames(), "device_auth_mode": a.authMode})

	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		fatal error
	)
	for _, u := range a.units {
		srv := gateway.New(u.proto, u.deps)
		if it, ok := u.proto.(gateway.IdleTimeoutProvider); ok {
			srv.SetIdleTimeout(it.IdleTimeout())
		}
		wg.Add(1)
		go func(u *unitRuntime, srv *gateway.Server) {
			defer wg.Done()
			if err := srv.ListenAndServe(ctx); err != nil {
				a.log.Error(map[string]any{"event": "fatal", "unit": u.name, "error": err.Error()})
				mu.Lock()
				if fatal == nil {
					fatal = err
				}
				mu.Unlock()
				stop() // a bind failure brings the whole process down loudly
			}
		}(u, srv)
	}
	wg.Wait()

	if fatal != nil {
		return fatal
	}
	a.log.Info(map[string]any{"event": "stopped"})
	return nil
}

func (a *App) unitNames() []string {
	names := make([]string, len(a.units))
	for i, u := range a.units {
		names[i] = u.name
	}
	return names
}

// anyMappings reports whether any hosted unit drives its output from editable
// mappings (so the shared event-code picklist is worth seeding).
func (a *App) anyMappings() bool {
	for _, u := range a.units {
		if u.mp != nil {
			return true
		}
	}
	return false
}

// anyMedia reports whether any hosted unit serves video (so the HTTP API should
// expose the HLS/clips roots).
func (a *App) anyMedia() *unitRuntime {
	for _, u := range a.units {
		if u.media != nil {
			return u
		}
	}
	return nil
}

// streamAggregator implements httpapi.StreamLister across every video unit's
// media manager, so GET /api/streams and POST /api/streams/stop-all see all
// active live streams in one process.
type streamAggregator struct{ units []*unitRuntime }

func (a streamAggregator) ActiveStreams() []httpapi.ActiveStream {
	out := []httpapi.ActiveStream{}
	for _, u := range a.units {
		if u.media == nil {
			continue
		}
		for _, ls := range u.media.ActiveStreams() {
			out = append(out, httpapi.ActiveStream{
				Unit: u.name, Serial: ls.Serial, Camera: ls.Camera, Profile: ls.Profile, UptimeMs: ls.UptimeMs,
			})
		}
	}
	return out
}

func (a streamAggregator) StopAllStreams() int {
	n := 0
	for _, u := range a.units {
		if u.media != nil {
			n += u.media.StopAllLive()
		}
	}
	return n
}

// startStoreBackedServices wires everything that needs the database: per-unit
// editable mappings + unit settings (each with instant LISTEN/NOTIFY
// reload), and the global event-code picklist, telemetry webhooks, and live server
// settings.
func (a *App) startStoreBackedServices(ctx context.Context) {
	// Per-unit services.
	for _, u := range a.units {
		u := u
		if u.mp != nil {
			a.seedAndLoadMappings(ctx, u)
			go a.store.ListenForMappingChanges(ctx, func(changed string) {
				if changed == "" || changed == u.name {
					a.reloadMappings(ctx, u)
				}
			})
			if a.cfg.MappingRefreshSeconds > 0 {
				go a.refreshMappings(ctx, u, time.Duration(a.cfg.MappingRefreshSeconds)*time.Second)
			}
		}
		if u.cfgUnit != nil {
			a.seedAndLoadUnitSettings(ctx, u)
			go a.store.ListenForUnitSettingsChanges(ctx, func(changed string) {
				if changed == "" || changed == u.name {
					a.loadUnitSettings(ctx, u)
				}
			})
		}
	}

	// Global, once: the ACM Standard Event Codes picklist (only when some unit has
	// editable mappings).
	if a.anyMappings() {
		a.seedEventCodes(ctx)
	}

	// Telemetry webhooks: migrate the legacy single URL into the webhooks table on
	// first run, load the enabled set into the sink, reload instantly on change.
	a.seedWebhooks(ctx)
	a.applyWebhooks(ctx)
	go a.store.ListenForWebhookChanges(ctx, func(string) { a.applyWebhooks(ctx) })

	// Record each unit's bound port(s) so the panel can flag a pending restart when
	// the configured port differs from the running one.
	for _, u := range a.units {
		if err := a.store.SetSetting(ctx, httpapi.DevicePortActiveKey(u.name), strconv.Itoa(u.port)); err != nil {
			a.log.Debug(map[string]any{"event": "device_port_active_write_failed", "unit": u.name, "error": err.Error()})
		}
		if u.media != nil {
			if err := a.store.SetSetting(ctx, httpapi.MediaPortActiveKey(u.name), strconv.Itoa(u.mediaPort)); err != nil {
				a.log.Debug(map[string]any{"event": "media_port_active_write_failed", "unit": u.name, "error": err.Error()})
			}
		}
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

// startHTTPAPI builds and runs the management/control HTTP API for every unit.
func (a *App) startHTTPAPI(ctx context.Context) {
	var verifier httpapi.KeyVerifier
	var data httpapi.DataStore
	if a.store != nil {
		verifier = a.store
		data = a.store
	}
	units := make([]httpapi.UnitInfo, len(a.units))
	for i, u := range a.units {
		ui := httpapi.UnitInfo{Name: u.name, Caps: u.caps}
		if u.cfgUnit != nil {
			ui.Schema = u.cfgUnit.SettingsSchema()
		}
		units[i] = ui
	}
	api := httpapi.New(a.cfg.ListenHost, a.cfg.HTTPPort, units, verifier, data, a.hub, a.log)
	api.SetInternalToken(a.cfg.InternalAPIToken)
	if a.cfg.InternalAPIToken != "" {
		a.log.Info(map[string]any{"event": "internal_token_enabled"})
	}
	if vu := a.anyMedia(); vu != nil {
		api.SetHLSRoot(a.cfg.HLSRoot)
		if vu.clips != nil {
			api.SetClipsRoot(a.cfg.ClipsRoot)
		}
		// Forward playlist fetches to every video unit's media manager as the
		// viewer-liveness signal for the reaper; each ignores paths it doesn't own.
		api.SetPlaylistObserver(func(relPath string) {
			for _, u := range a.units {
				if u.media != nil {
					u.media.TouchPlaylistPath(relPath)
				}
			}
		})
		// Active-stream count / stop-all, aggregated across every video unit.
		api.SetStreamLister(streamAggregator{units: a.units})
	}
	go func() {
		if err := api.Run(ctx); err != nil {
			a.log.Error(map[string]any{"event": "http_fatal", "error": err.Error()})
		}
	}()
}

// effectiveCapabilities is what the running gateway actually offers for one unit:
// the unit's declared capabilities gated by runtime configuration.
func (a *App) effectiveCapabilities(u *unitRuntime) gateway.EffectiveCapabilities {
	caps := u.proto.Capabilities()
	return gateway.EffectiveCapabilities{
		HasVideo:    caps.HasVideo && a.cfg.VideoEnabled(),
		HasCommands: caps.HasCommands,
		HasConfig:   caps.HasConfig,
		HasStatus:   caps.HasStatus,
		HasClips:    u.media != nil && u.clips != nil,
		HasMappings: u.mp != nil,
	}
}

// seedAndLoadMappings seeds a unit's built-in mapping defaults into the database
// (no-op for existing rows) and applies the current set to the unit's live maps.
func (a *App) seedAndLoadMappings(ctx context.Context, u *unitRuntime) {
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	// Drop rows the unit no longer honors (handled by built-in logic) that older
	// builds seeded, so the admin only shows mappings that take effect.
	if pruner, ok := u.mp.(gateway.MappingPruner); ok {
		for _, p := range pruner.PrunableMappings() {
			n, err := a.store.PruneEventMappings(cctx, u.name, p.MapType, p.Codes)
			if err != nil {
				a.log.Error(map[string]any{"event": "mapping_prune_failed", "unit": u.name, "map_type": p.MapType, "error": err.Error()})
			} else if n > 0 {
				a.log.Info(map[string]any{"event": "mappings_pruned", "unit": u.name, "map_type": p.MapType, "rows": n})
			}
		}
	}
	if err := a.store.SeedEventMappings(cctx, u.name, u.mp.DefaultMappingEntries()); err != nil {
		a.log.Error(map[string]any{"event": "mapping_seed_failed", "unit": u.name, "error": err.Error()})
	}
	loaded, err := a.store.LoadEventMappings(cctx, u.name)
	if err != nil {
		a.log.Error(map[string]any{"event": "mapping_load_failed", "unit": u.name, "error": err.Error(), "detail": "using built-in defaults"})
		return
	}
	u.mp.ApplyMappings(loaded)
	a.log.Info(map[string]any{"event": "mappings_loaded", "unit": u.name, "models": len(loaded)})
}

// seedEventCodes loads the embedded ACM Standard Event Codes into the database so
// the front end can offer them as a picklist. Idempotent (upsert). Global.
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

// reloadMappings loads a unit's current mappings and applies them to its live maps.
func (a *App) reloadMappings(ctx context.Context, u *unitRuntime) {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	loaded, err := a.store.LoadEventMappings(cctx, u.name)
	if err != nil {
		a.log.Debug(map[string]any{"event": "mapping_reload_failed", "unit": u.name, "error": err.Error()})
		return
	}
	u.mp.ApplyMappings(loaded)
	a.log.Debug(map[string]any{"event": "mappings_reloaded", "unit": u.name, "models": len(loaded)})
}

// refreshMappings periodically reloads a unit's mappings as a safety net behind
// LISTEN/NOTIFY (covers a missed notification on a dropped connection).
func (a *App) refreshMappings(ctx context.Context, u *unitRuntime, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.reloadMappings(ctx, u)
		}
	}
}

// seedAndLoadUnitSettings seeds a unit's schema defaults into the database (no-op
// for existing rows) and loads the stored values into the unit's settings holder.
func (a *App) seedAndLoadUnitSettings(ctx context.Context, u *unitRuntime) {
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	for _, f := range u.cfgUnit.SettingsSchema() {
		if err := a.store.SeedUnitSettingDefault(cctx, u.name, f.Key, f.Default); err != nil {
			a.log.Error(map[string]any{"event": "unit_setting_seed_failed", "unit": u.name, "key": f.Key, "error": err.Error()})
		}
	}
	a.loadUnitSettings(ctx, u)
}

// loadUnitSettings loads a unit's stored settings and hot-swaps them into its holder.
func (a *App) loadUnitSettings(ctx context.Context, u *unitRuntime) {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	m, err := a.store.LoadUnitSettings(cctx, u.name)
	if err != nil {
		a.log.Debug(map[string]any{"event": "unit_settings_load_failed", "unit": u.name, "error": err.Error()})
		return
	}
	u.settings.Replace(m)
	a.log.Debug(map[string]any{"event": "unit_settings_loaded", "unit": u.name, "keys": len(m)})
}

// applyGatewayName loads the stored gateway identifier and installs it on the
// message builder so every universal message carries it.
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
// store so it takes effect for subsequent device connections.
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
// first run, preferring a value previously set via the legacy webhook_url setting,
// falling back to DEVICE_WEBHOOK_URL. No-op once any webhook exists.
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
// targets.
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

// Package httpapi is the gateway's management/control HTTP API. It is the ONLY
// way the admin panel reaches gateway state — the panel never touches the
// database directly. Every protected request must carry
// `Authorization: Bearer <api-key>`.
package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dfm/device-gateway/internal/core/gateway"
	"github.com/dfm/device-gateway/internal/core/logging"
	"github.com/dfm/device-gateway/internal/core/mapping"
)

// KeyVerifier validates an API key. The postgres store implements it.
type KeyVerifier interface {
	VerifyAPIKey(ctx context.Context, key string) (bool, error)
}

// DataStore is the gateway state the admin panel reads and edits through the
// API. The postgres store implements it. It is optional: when nil, the
// data-backed endpoints respond 503 (no database configured).
type DataStore interface {
	VerifyUser(ctx context.Context, email, password string) (bool, error)
	CountUsers(ctx context.Context) (int, error)

	ListDevices(ctx context.Context) ([]map[string]any, error)
	ListPendingDevices(ctx context.Context) ([]map[string]any, error)
	ApproveDevice(ctx context.Context, serial, fallbackProtocol string) error
	RejectDevice(ctx context.Context, serial string) error
	DeleteDevice(ctx context.Context, serial string) error

	ListEventMappings(ctx context.Context, unit, model string) ([]map[string]any, error)
	ListEventMappingModels(ctx context.Context, unit string) ([]string, error)
	UpsertEventMapping(ctx context.Context, unit string, e mapping.Entry) error
	DeleteEventMapping(ctx context.Context, unit, model, mapType string, code int) error
	CopyEventMappings(ctx context.Context, unit, fromModel, toModel string) error

	ListGatewayErrors(ctx context.Context, limit, offset int) ([]map[string]any, error)
	ListDeviceErrors(ctx context.Context, limit, offset int) ([]map[string]any, error)

	ListStandardEventCodes(ctx context.Context) ([]map[string]any, error)
	AddStandardEventCode(ctx context.Context, code, category, notes string) error

	ListSettings(ctx context.Context) ([]map[string]any, error)
	SetSetting(ctx context.Context, key, value string) error

	ListUnitSettings(ctx context.Context, unit string) ([]map[string]any, error)
	SetUnitSetting(ctx context.Context, unit, key, value string) error

	ListWebhooks(ctx context.Context) ([]map[string]any, error)
	CreateWebhook(ctx context.Context, name, url string, enabled bool) (int64, error)
	UpdateWebhook(ctx context.Context, id int64, name, url string, enabled bool) error
	DeleteWebhook(ctx context.Context, id int64) error

	ListUsers(ctx context.Context) ([]map[string]any, error)
	CreateUser(ctx context.Context, email, password string) error
	SetUserActive(ctx context.Context, id int64, active bool) error
	SetUserPassword(ctx context.Context, id int64, password string) error
	DeleteUser(ctx context.Context, id int64) error

	ListAPIKeyMeta(ctx context.Context) ([]map[string]any, error)
	CreateAPIKey(ctx context.Context, name string) (string, error)
	RevokeAPIKey(ctx context.Context, prefix string) (int64, error)

	ListClips(ctx context.Context, serial string, limit, offset int) ([]map[string]any, error)
	GetClip(ctx context.Context, id int64) (map[string]any, error)
	DeleteClip(ctx context.Context, id int64) (string, error)

	ListSnapshots(ctx context.Context, serial string, limit, offset int) ([]map[string]any, error)
	GetSnapshot(ctx context.Context, id int64) (map[string]any, error)
	CreateSnapshot(ctx context.Context, serial string, camera int, kind, source string, capturedUTC int64, devicePath, storagePath string, fileSize int64) (int64, error)
	DeleteSnapshot(ctx context.Context, id int64) (string, error)
}

// Setting keys httpapi validates specially (mirror the postgres.Setting* consts).
const (
	settingWebhookURL          = "webhook_url"
	settingDevicePort          = "device_port"
	settingGatewayName         = "gateway_name"
	settingDeviceRejectUnknown = "device_reject_unknown"
)

// errNotFound mirrors postgres.ErrNotFound without importing it (the store
// satisfies DataStore structurally). Handlers compare by message.
const notFoundMsg = "not found"

// Per-unit port settings live in the global server_settings table, namespaced by
// unit so each listener (and a video unit's media port) has its own admin-editable
// port. The *Active keys record the port the gateway actually bound on startup, so
// the panel can flag a pending restart. Shared with the app runner.
func DevicePortKey(unit string) string       { return "device_port:" + unit }
func DevicePortActiveKey(unit string) string { return "device_port_active:" + unit }
func MediaPortKey(unit string) string        { return "media_port:" + unit }
func MediaPortActiveKey(unit string) string  { return "media_port_active:" + unit }

// capKey is the server_settings key for a unit's per-capability enable toggle.
// Absent = enabled; "false" = the operator disabled a supported feature. A
// capability the unit doesn't support can never be enabled.
func capKey(feature, unit string) string { return "cap_" + feature + ":" + unit }

// reportedCaps gates a unit's SUPPORTED capabilities (declared by its protocol AND
// enabled by runtime config) by the operator's disable-toggles, yielding what the
// admin should actually show. Clips follow video; mappings are not toggleable.
func reportedCaps(sup gateway.EffectiveCapabilities, unit string, toggles map[string]string) gateway.EffectiveCapabilities {
	on := func(feature string, supported bool) bool {
		if !supported {
			return false
		}
		if v, ok := toggles[capKey(feature, unit)]; ok && v == "false" {
			return false
		}
		return true
	}
	video := on("video", sup.HasVideo)
	return gateway.EffectiveCapabilities{
		HasVideo:     video,
		HasCommands:  on("commands", sup.HasCommands),
		HasConfig:    on("config", sup.HasConfig),
		HasStatus:    on("status", sup.HasStatus),
		HasClips:     sup.HasClips && video,
		HasMappings:  sup.HasMappings,
		HasSnapshots: sup.HasSnapshots && video, // follows video; was dropped here, hiding Howen's capture UI
	}
}

// loadCapToggles reads the per-unit capability disable-toggles from settings.
// Returns an empty map when there's no database.
func (s *Server) loadCapToggles(ctx context.Context) map[string]string {
	out := map[string]string{}
	if s.data == nil {
		return out
	}
	rows, err := s.data.ListSettings(ctx)
	if err != nil {
		return out
	}
	for _, row := range rows {
		k, _ := row["key"].(string)
		if strings.HasPrefix(k, "cap_") {
			v, _ := row["value"].(string)
			out[k] = v
		}
	}
	return out
}

// UnitInfo describes one hosted unit type for the admin panel: its name, the
// effective capabilities it offers right now, and (optionally) its editable
// settings schema. The gateway hosts one or more of these.
type UnitInfo struct {
	Name   string                        `json:"unit"`
	Caps   gateway.EffectiveCapabilities `json:"capabilities"`
	Schema []gateway.SettingField        `json:"schema,omitempty"`
}

// Server is the HTTP API server.
type Server struct {
	addr             string
	units            []UnitInfo // hosted unit types
	defaultUnit      string     // units[0].Name — back-compat default for unit-scoped routes
	log              *logging.Logger
	verifier         KeyVerifier
	data             DataStore
	hub              *gateway.Hub
	internalToken    string
	hlsRoot          string
	clipsRoot        string
	playlistObserver func(relPath string) // notified when a viewer fetches an HLS playlist
	streams          StreamLister         // enumerate/stop active live streams; nil when no unit has video
	ports            PortLister           // device-facing ports to report a listening check for
	srv              *http.Server
}

// PortInfo is one device-facing TCP port the gateway listens on.
type PortInfo struct {
	Unit string `json:"unit"`
	Kind string `json:"kind"` // "control" | "media"
	Port int    `json:"port"`
}

// PortLister reports the device-facing ports the gateway is configured to listen
// on (per unit's resolved control/media port). Implemented by the app.
type PortLister interface {
	DevicePorts() []PortInfo
}

// ActiveStream is one running live stream, surfaced by GET /api/streams.
type ActiveStream struct {
	Unit     string `json:"unit"`
	Serial   string `json:"serial"`
	Camera   int    `json:"camera"`
	Profile  int    `json:"profile"`
	UptimeMs int64  `json:"uptime_ms"`
}

// StreamLister enumerates and stops active live streams across all video units.
// The app implements it by aggregating the per-unit media managers.
type StreamLister interface {
	ActiveStreams() []ActiveStream
	StopAllStreams() int
}

// unitNames returns the set of hosted unit-type names.
func (s *Server) unitNames() map[string]bool {
	set := make(map[string]bool, len(s.units))
	for _, u := range s.units {
		set[u.Name] = true
	}
	return set
}

// unitInfo returns the hosted unit by name.
func (s *Server) unitInfo(name string) (UnitInfo, bool) {
	for _, u := range s.units {
		if u.Name == name {
			return u, true
		}
	}
	return UnitInfo{}, false
}

// SetInternalToken sets the shared secret the admin panel uses to authenticate
// (accepted alongside DB API keys). Empty disables it.
func (s *Server) SetInternalToken(token string) { s.internalToken = token }

// SetHLSRoot enables HLS file serving (GET /api/hls/...) from dir. Empty disables
// it (the route responds 404).
func (s *Server) SetHLSRoot(dir string) { s.hlsRoot = dir }

// SetClipsRoot sets the directory recorded-clip .mp4 files are stored under (the
// bucket), used by the clip download handler.
func (s *Server) SetClipsRoot(dir string) { s.clipsRoot = dir }

// SetPlaylistObserver registers a callback invoked with the playlist's path
// (relative to the HLS root) each time a viewer fetches an HLS playlist. The
// media reaper uses this as the "viewer still watching" signal so it can stop a
// live stream the browser walked away from.
func (s *Server) SetPlaylistObserver(fn func(relPath string)) { s.playlistObserver = fn }

// SetStreamLister wires the active-stream enumerator/stopper used by
// GET /api/streams and POST /api/streams/stop-all. Empty/nil = no active streams.
func (s *Server) SetStreamLister(l StreamLister) { s.streams = l }

// SetPortLister wires the device-facing port list reported by GET /api/ports.
func (s *Server) SetPortLister(l PortLister) { s.ports = l }

// New builds the API server. units are the hosted unit types (name + effective
// capabilities + optional settings schema); the first is the back-compat default
// for unit-scoped routes that omit a unit. If verifier is nil, protected routes
// respond 503. If data is nil, the data-backed endpoints respond 503 while
// connected-device endpoints still work via the hub.
func New(host string, port int, units []UnitInfo, verifier KeyVerifier, data DataStore, hub *gateway.Hub, log *logging.Logger) *Server {
	defaultUnit := ""
	if len(units) > 0 {
		defaultUnit = units[0].Name
	}
	s := &Server{
		addr:        net.JoinHostPort(host, itoa(port)),
		units:       units,
		defaultUnit: defaultUnit,
		log:         log.With("http"),
		verifier:    verifier,
		data:        data,
		hub:         hub,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)

	// Protected route group. All inherit the API-key check.
	protected := map[string]http.HandlerFunc{
		"GET /api/ping": s.handlePing,

		// This gateway's unit type + effective capabilities (drives admin-panel UI).
		"GET /api/gateway/info": s.handleGatewayInfo,

		// Admin auth: verify a front-end user's credentials (the BFF calls this
		// holding the API key, then issues its own session cookie).
		"POST /api/auth/login": s.handleLogin,

		// First-run setup (only effective while there are zero users).
		"GET /api/setup/status": s.handleSetupStatus,
		"POST /api/setup":       s.handleSetup,

		// User accounts.
		"GET /api/users":         s.handleListUsers,
		"POST /api/users":        s.handleCreateUser,
		"PUT /api/users/{id}":    s.handleUpdateUser,
		"DELETE /api/users/{id}": s.handleDeleteUser,

		// API keys for external API access (Bearer keys).
		"GET /api/api-keys":             s.handleListAPIKeys,
		"POST /api/api-keys":            s.handleCreateAPIKey,
		"DELETE /api/api-keys/{prefix}": s.handleRevokeAPIKey,

		// Connected devices (live, via the hub).
		"GET /api/units":                    s.handleListUnits,
		"GET /api/units/{serial}":           s.handleGetUnit,
		"GET /api/units/{serial}/status":    s.handleUnitStatus,
		"GET /api/units/{serial}/config":    s.handleGetConfig,
		"PUT /api/units/{serial}/config":    s.handleUpdateConfig,
		"POST /api/units/{serial}/commands": s.handleCommand,

		// Live video.
		"POST /api/units/{serial}/stream/start": s.handleStreamStart,
		"POST /api/units/{serial}/stream/stop":  s.handleStreamStop,
		"GET /api/hls/":                         s.handleHLS,
		// Active live streams across all units (count / stop-all).
		"GET /api/streams":           s.handleStreamsList,
		"POST /api/streams/stop-all": s.handleStreamsStopAll,
		// Device-facing port listeners + a self-check that each is accepting.
		"GET /api/ports": s.handlePortsList,

		// Discover what footage a device has (file query) before requesting a clip.
		"GET /api/units/{serial}/recordings": s.handleQueryRecordings,
		// Recorded clips (request a download, then poll status / download the .mp4).
		"POST /api/units/{serial}/snapshots":       s.handleSnapshotRequest,
		"POST /api/units/{serial}/snapshot/image":  s.handleSnapshotImage,
		"GET /api/units/{serial}/snapshots/search": s.handleSnapshotSearch,
		"GET /api/units/{serial}/snapshots/file":   s.handleSnapshotFile,
		"POST /api/units/{serial}/snapshots/save":  s.handleSnapshotSave,
		"GET /api/snapshots":                       s.handleListSnapshots,
		"GET /api/snapshots/{id}/download":         s.handleSnapshotDownload,
		"DELETE /api/snapshots/{id}":               s.handleDeleteSnapshot,
		"POST /api/units/{serial}/clips":           s.handleClipRequest,
		"GET /api/clips":                           s.handleListClips,
		"GET /api/clips/{id}":                      s.handleGetClip,
		"GET /api/clips/{id}/download":             s.handleClipDownload,
		"DELETE /api/clips/{id}":                   s.handleDeleteClip,

		// Device approval (registry, via the store).
		"GET /api/devices":                   s.handleListDevices,
		"GET /api/devices/pending":           s.handleListPending,
		"POST /api/devices/{serial}/approve": s.handleApproveDevice,
		"POST /api/devices/{serial}/reject":  s.handleRejectDevice,
		"DELETE /api/devices/{serial}":       s.handleDeleteDevice,

		// Server settings: editable event mappings (instant via NOTIFY).
		"GET /api/mappings":        s.handleListMappings,
		"PUT /api/mappings":        s.handleUpsertMapping,
		"DELETE /api/mappings":     s.handleDeleteMapping,
		"GET /api/mappings/models": s.handleListMappingModels,
		"POST /api/mappings/copy":  s.handleCopyMappings,

		// Editable server settings (global).
		"GET /api/settings": s.handleListSettings,
		"PUT /api/settings": s.handleSetSetting,

		// Per-unit-type settings (a unit's own editable gateway-side settings,
		// distinct from per-device parameter config). Schema is declared by the unit.
		"GET /api/unit-types/{unit}/settings/schema": s.handleUnitSettingsSchema,
		"GET /api/unit-types/{unit}/settings":        s.handleListUnitSettings,
		"PUT /api/unit-types/{unit}/settings":        s.handleSetUnitSetting,

		// Per-unit listener ports (device port, plus media port for video units).
		// Applied on restart — a bound TCP listener can't move at runtime.
		"GET /api/unit-types/{unit}/ports": s.handleGetPorts,
		"PUT /api/unit-types/{unit}/ports": s.handleSetPorts,

		// Per-unit capability disable-toggles (turn off a supported feature so the
		// admin hides it). Applies live; can't enable an unsupported feature.
		"PUT /api/unit-types/{unit}/capabilities": s.handleSetCapabilities,

		// Telemetry webhooks (the GPS/event data sinks).
		"GET /api/webhooks":         s.handleListWebhooks,
		"POST /api/webhooks":        s.handleCreateWebhook,
		"PUT /api/webhooks/{id}":    s.handleUpdateWebhook,
		"DELETE /api/webhooks/{id}": s.handleDeleteWebhook,

		// Reference data.
		"GET /api/event-codes":  s.handleEventCodes,
		"POST /api/event-codes": s.handleAddEventCode,

		// Logs.
		"GET /api/logs":          s.handleGatewayErrors,
		"GET /api/device-errors": s.handleDeviceErrors,

		// Live activity log: an in-memory tail of the gateway's own log stream
		// (connects, approvals, GPS forwards, ACKs, errors). Independent of the DB.
		"GET /api/logs/live":  s.handleLiveLogs,
		"GET /api/logs/level": s.handleGetLogLevel,
		"PUT /api/logs/level": s.handleSetLogLevel,
	}
	for pattern, h := range protected {
		mux.Handle(pattern, s.requireAPIKey(h))
	}

	s.srv = &http.Server{
		Addr:              s.addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	return s
}

// Run serves until ctx is cancelled, then shuts down gracefully.
func (s *Server) Run(ctx context.Context) error {
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.srv.Shutdown(shutCtx)
	}()
	s.log.Info(map[string]any{"event": "http_listening", "addr": s.addr, "protected": s.verifier != nil, "data": s.data != nil})
	if err := s.srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "unit": s.defaultUnit})
}

func (s *Server) handlePing(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// GET /api/gateway/info — the hosted unit types and each one's effective
// capabilities (declared by the unit AND enabled by config). The admin panel reads
// it once to render per-unit UI and hide features this build/config doesn't offer.
// `unit`/`capabilities` echo the first unit for backward compatibility.
func (s *Server) handleGatewayInfo(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	toggles := s.loadCapToggles(ctx)

	units := make([]map[string]any, 0, len(s.units))
	for _, u := range s.units {
		units = append(units, map[string]any{
			"unit":         u.Name,
			"capabilities": reportedCaps(u.Caps, u.Name, toggles), // effective (drives UI)
			"supported":    u.Caps,                                // what the unit can do (which toggles to show)
			"schema":       u.Schema,
		})
	}
	resp := map[string]any{"units": units}
	if len(s.units) > 0 {
		resp["unit"] = s.units[0].Name
		resp["capabilities"] = reportedCaps(s.units[0].Caps, s.units[0].Name, toggles)
	}
	writeJSON(w, http.StatusOK, resp)
}

// POST /api/auth/login — verify a front-end user's email/password.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if s.data == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "user store unavailable"})
		return
	}
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON body"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	ok, err := s.data.VerifyUser(ctx, body.Email, body.Password)
	if err != nil {
		s.log.Error(map[string]any{"event": "login_error", "error": err.Error()})
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "auth error"})
		return
	}
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid credentials"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "email": strings.TrimSpace(body.Email)})
}

// GET /api/setup/status — report whether first-run setup is needed (no users).
func (s *Server) handleSetupStatus(w http.ResponseWriter, r *http.Request) {
	if !s.dataReady(w) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	n, err := s.data.CountUsers(ctx)
	if err != nil {
		s.dataError(w, "setup_status", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"needs_setup": n == 0, "initialized": n > 0})
}

// POST /api/setup — first-run bootstrap: create the first admin and optionally
// set the gateway name and a webhook. Refused (409) once any user exists.
func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	if !s.dataReady(w) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	n, err := s.data.CountUsers(ctx)
	if err != nil {
		s.dataError(w, "setup_count", err)
		return
	}
	if n > 0 {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "already initialized"})
		return
	}

	var body struct {
		Email       string `json:"email"`
		Password    string `json:"password"`
		GatewayName string `json:"gateway_name"`
		WebhookURL  string `json:"webhook_url"`
		DevicePort  string `json:"device_port"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON body"})
		return
	}
	if !validEmail(body.Email) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "a valid email is required"})
		return
	}
	if u := strings.TrimSpace(body.WebhookURL); u != "" && !validHTTPURL(u) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "webhook_url must be a valid http(s) URL"})
		return
	}
	if p := strings.TrimSpace(body.DevicePort); p != "" {
		if n, err := strconv.Atoi(p); err != nil || n < 1 || n > 65535 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "device_port must be a whole number between 1 and 65535"})
			return
		}
	}

	if err := s.data.CreateUser(ctx, body.Email, body.Password); err != nil {
		s.writeUserError(w, "setup_create_user", err)
		return
	}
	if g := strings.TrimSpace(body.GatewayName); g != "" {
		if err := s.data.SetSetting(ctx, settingGatewayName, g); err != nil {
			s.log.Error(map[string]any{"event": "setup_gateway_name_error", "error": err.Error()})
		}
	}
	if p := strings.TrimSpace(body.DevicePort); p != "" {
		if err := s.data.SetSetting(ctx, settingDevicePort, p); err != nil {
			s.log.Error(map[string]any{"event": "setup_device_port_error", "error": err.Error()})
		}
	}
	if u := strings.TrimSpace(body.WebhookURL); u != "" {
		if _, err := s.data.CreateWebhook(ctx, "default", u, true); err != nil {
			s.log.Error(map[string]any{"event": "setup_webhook_error", "error": err.Error()})
		}
	}
	s.log.Info(map[string]any{"event": "setup_completed", "email": strings.TrimSpace(body.Email)})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "email": strings.TrimSpace(body.Email)})
}

// streamBody is the start/stop request: which camera + quality profile.
type streamBody struct {
	Camera  int `json:"camera"`  // 0-based camera index
	Profile int `json:"profile"` // 0 = main (high), 1 = sub (low)
}

// POST /api/units/{serial}/stream/start — begin a live HLS stream.
func (s *Server) handleStreamStart(w http.ResponseWriter, r *http.Request) {
	serial := r.PathValue("serial")
	if s.hub == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "unit not connected"})
		return
	}
	vc, ok := s.hub.Video(serial)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "unit not connected or video unavailable"})
		return
	}
	var body streamBody
	if err := decodeJSON(w, r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON body"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	info, err := vc.StartLive(ctx, body.Camera, body.Profile)
	if err != nil {
		s.writeStreamError(w, err)
		return
	}
	// The device has accepted the command, but ffmpeg only writes the playlist
	// once it has buffered the first ~2s segment (a few seconds after the device
	// actually starts sending frames). Wait for it so the browser never races a
	// not-yet-existent manifest and gets a hard 404. `ready:false` just means the
	// playlist wasn't up within the window — the player can still keep retrying.
	ready := s.waitHLSReady(ctx, info.HLSPath)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "session_id": info.SessionID, "hls_path": info.HLSPath, "ready": ready,
	})
}

// waitHLSReady blocks until ffmpeg has written the stream's playlist (i.e. the
// first HLS segment is on disk) or ctx is done. Returns true if the playlist now
// exists and is non-empty.
func (s *Server) waitHLSReady(ctx context.Context, hlsPath string) bool {
	if s.hlsRoot == "" || hlsPath == "" {
		return false
	}
	file := path.Join(s.hlsRoot, hlsPath)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		if fi, err := os.Stat(file); err == nil && fi.Size() > 0 {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
		}
	}
}

// POST /api/units/{serial}/stream/stop — stop a live stream.
func (s *Server) handleStreamStop(w http.ResponseWriter, r *http.Request) {
	serial := r.PathValue("serial")
	if s.hub == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "unit not connected"})
		return
	}
	vc, ok := s.hub.Video(serial)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "unit not connected or video unavailable"})
		return
	}
	var body streamBody
	if err := decodeJSON(w, r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON body"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := vc.StopLive(ctx, body.Camera, body.Profile); err != nil {
		s.writeStreamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// GET /api/streams — list the live streams currently running across all units.
func (s *Server) handleStreamsList(w http.ResponseWriter, r *http.Request) {
	list := []ActiveStream{}
	if s.streams != nil {
		if active := s.streams.ActiveStreams(); active != nil {
			list = active
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"streams": list, "count": len(list)})
}

// POST /api/streams/stop-all — stop every active live stream (e.g. to free server
// resources). Returns how many were stopped. Clips are unaffected.
func (s *Server) handleStreamsStopAll(w http.ResponseWriter, r *http.Request) {
	stopped := 0
	if s.streams != nil {
		stopped = s.streams.StopAllStreams()
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "stopped": stopped})
}

// GET /api/ports — each device-facing port the gateway listens on, with a
// listening self-check (a short loopback dial confirming the listener accepts).
// Checks the container-internal listener, so it catches a crashed/unbound port
// but not a Docker-publish or firewall mismatch.
func (s *Server) handlePortsList(w http.ResponseWriter, r *http.Request) {
	type portStatus struct {
		PortInfo
		Listening bool `json:"listening"`
	}
	out := []portStatus{}
	if s.ports != nil {
		list := s.ports.DevicePorts()
		out = make([]portStatus, len(list))
		var wg sync.WaitGroup
		for i, p := range list {
			wg.Add(1)
			go func(i int, p PortInfo) {
				defer wg.Done()
				out[i] = portStatus{PortInfo: p, Listening: portListening(p.Port)}
			}(i, p)
		}
		wg.Wait()
	}
	writeJSON(w, http.StatusOK, map[string]any{"ports": out})
}

// portListening reports whether something is accepting TCP connections on the
// gateway's own port (loopback dial, short timeout).
func portListening(port int) bool {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func (s *Server) writeStreamError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, gateway.ErrDeviceSleeping):
		writeJSON(w, http.StatusConflict, map[string]any{"error": "device is in standby — wake it first", "code": "device_sleeping"})
	case errors.Is(err, gateway.ErrCommandTimeout):
		writeJSON(w, http.StatusGatewayTimeout, map[string]any{"error": "device did not respond in time (it may be in standby)"})
	case strings.Contains(err.Error(), "not enabled"):
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": err.Error()})
	case strings.Contains(err.Error(), "invalid") || strings.Contains(err.Error(), "not approved"):
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
	default:
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
	}
}

// GET /api/hls/<serial>/<camera>/<profile>/(stream.m3u8|seg_NNN.ts) — serve the
// HLS playlist/segments produced by ffmpeg. API-key protected like everything
// else; the admin panel proxies these so the browser player stays authenticated.
func (s *Server) handleHLS(w http.ResponseWriter, r *http.Request) {
	if s.hlsRoot == "" {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "video not enabled"})
		return
	}
	switch path.Ext(r.URL.Path) {
	case ".m3u8":
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Header().Set("Cache-Control", "no-cache")
		// A playlist fetch means a viewer is still watching — feed the media
		// reaper so it doesn't stop a stream that's actively being played.
		if s.playlistObserver != nil {
			s.playlistObserver(strings.TrimPrefix(r.URL.Path, "/api/hls/"))
		}
	case ".ts":
		w.Header().Set("Content-Type", "video/mp2t")
	}
	http.StripPrefix("/api/hls/", http.FileServer(http.Dir(s.hlsRoot))).ServeHTTP(w, r)
}

// GET /api/units/{serial}/recordings?camera=&profile=&start_utc=&end_utc= — ask
// the device what footage it has for the window (defaults: all cameras, last 24h).
func (s *Server) handleQueryRecordings(w http.ResponseWriter, r *http.Request) {
	serial := r.PathValue("serial")
	if s.hub == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "unit not connected"})
		return
	}
	vc, ok := s.hub.Video(serial)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "unit not connected or video unavailable"})
		return
	}
	q := r.URL.Query()
	camera := -1
	if v := q.Get("camera"); v != "" {
		camera, _ = strconv.Atoi(v)
	}
	profile := 1
	if v := q.Get("profile"); v != "" {
		profile, _ = strconv.Atoi(v)
	}
	end, _ := strconv.ParseInt(q.Get("end_utc"), 10, 64)
	start, _ := strconv.ParseInt(q.Get("start_utc"), 10, 64)
	if end <= 0 {
		end = time.Now().Unix()
	}
	if start <= 0 {
		start = end - 24*60*60
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	recs, err := vc.QueryRecordings(ctx, camera, profile, start, end)
	if err != nil {
		s.writeStreamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"recordings": recs, "count": len(recs)})
}

// POST /api/units/{serial}/snapshots — ask the device to capture a still image on
// one or more camera channels (H-Protocol 0x4020). The response reports the
// device-side file paths; fetching the JPEG bytes is a later milestone.
//
// Body: {"channels": [0,1], "resolution": 0}  (channels 0-based; default [0].
// resolution: 0 follow-video, 1 1080, 2 720, 3 VGA, 4 D1.)
func (s *Server) handleSnapshotRequest(w http.ResponseWriter, r *http.Request) {
	serial := r.PathValue("serial")
	if s.hub == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "unit not connected"})
		return
	}
	snap, ok := s.hub.Snapshotter(serial)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "unit not connected or snapshots unavailable"})
		return
	}
	var req struct {
		Channels   []int `json:"channels"`
		Resolution int   `json:"resolution"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON body"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	res, err := snap.RequestSnapshot(ctx, req.Channels, req.Resolution)
	if err != nil {
		s.writeStreamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "session_id": res.SessionID, "files": res.Files})
}

// POST /api/units/{serial}/snapshot/image?camera=0&resolution=0 — capture a still
// on one camera and return the JPEG bytes inline (the device captures, then
// uploads the file over the media port). Needs video/media enabled on the gateway.
func (s *Server) handleSnapshotImage(w http.ResponseWriter, r *http.Request) {
	serial := r.PathValue("serial")
	if s.hub == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "unit not connected"})
		return
	}
	snap, ok := s.hub.Snapshotter(serial)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "unit not connected or snapshots unavailable"})
		return
	}
	q := r.URL.Query()
	camera := 0
	if v := q.Get("camera"); v != "" {
		camera, _ = strconv.Atoi(v)
	}
	resolution := 0
	if v := q.Get("resolution"); v != "" {
		resolution, _ = strconv.Atoi(v)
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	img, err := snap.CaptureImage(ctx, camera, resolution)
	if err != nil {
		s.writeStreamError(w, err)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Content-Length", strconv.Itoa(len(img)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(img)
}

// GET /api/units/{serial}/snapshots/search?camera=&start_utc=&end_utc=&kind= —
// list stills the device already has on its SD card for a window. camera 0-based
// (omit/-1 = all); kind `general` (default) or `alarm`; times are UTC seconds.
func (s *Server) handleSnapshotSearch(w http.ResponseWriter, r *http.Request) {
	serial := r.PathValue("serial")
	if s.hub == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "unit not connected"})
		return
	}
	snap, ok := s.hub.Snapshotter(serial)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "unit not connected or snapshots unavailable"})
		return
	}
	q := r.URL.Query()
	camera := -1
	if v := q.Get("camera"); v != "" {
		camera, _ = strconv.Atoi(v)
	}
	start, _ := strconv.ParseInt(q.Get("start_utc"), 10, 64)
	end, _ := strconv.ParseInt(q.Get("end_utc"), 10, 64)
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	files, err := snap.SearchSnapshots(ctx, camera, start, end, q.Get("kind"))
	if err != nil {
		s.writeStreamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"snapshots": files, "count": len(files)})
}

// GET /api/units/{serial}/snapshots/file?path=<device_path> — download one stored
// still by its device path (from a search result), returned as image/jpeg.
func (s *Server) handleSnapshotFile(w http.ResponseWriter, r *http.Request) {
	serial := r.PathValue("serial")
	if s.hub == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "unit not connected"})
		return
	}
	snap, ok := s.hub.Snapshotter(serial)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "unit not connected or snapshots unavailable"})
		return
	}
	path := r.URL.Query().Get("path")
	if strings.TrimSpace(path) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing path query parameter"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	img, err := snap.FetchSnapshotFile(ctx, path)
	if err != nil {
		s.writeStreamError(w, err)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Content-Length", strconv.Itoa(len(img)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(img)
}

// POST /api/units/{serial}/snapshots/save — capture (or copy a device-stored)
// snapshot and persist it to the gateway: the JPEG is written under
// CLIPS_ROOT/snapshots and a row is recorded in the snapshots table.
//
// Body: { "source": "capture"|"device", "camera": 0, "resolution": 0,
//
//	"device_path": "...", "kind": "general", "captured_utc": 0 }
func (s *Server) handleSnapshotSave(w http.ResponseWriter, r *http.Request) {
	serial := r.PathValue("serial")
	if s.hub == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "unit not connected"})
		return
	}
	snap, ok := s.hub.Snapshotter(serial)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "unit not connected or snapshots unavailable"})
		return
	}
	if !s.dataReady(w) {
		return
	}
	if s.clipsRoot == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "snapshot storage not configured"})
		return
	}
	var req struct {
		Source      string `json:"source"`
		Camera      int    `json:"camera"`
		Resolution  int    `json:"resolution"`
		DevicePath  string `json:"device_path"`
		Kind        string `json:"kind"`
		CapturedUTC int64  `json:"captured_utc"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON body"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
	defer cancel()

	var (
		img         []byte
		err         error
		source      = "capture"
		kind        = "general"
		devicePath  string
		capturedUTC = time.Now().Unix()
	)
	if req.Source == "device" {
		if strings.TrimSpace(req.DevicePath) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "device_path required for source=device"})
			return
		}
		img, err = snap.FetchSnapshotFile(ctx, req.DevicePath)
		source, devicePath = "device", req.DevicePath
		if req.Kind != "" {
			kind = req.Kind
		}
		if req.CapturedUTC > 0 {
			capturedUTC = req.CapturedUTC
		}
	} else {
		img, err = snap.CaptureImage(ctx, req.Camera, req.Resolution)
	}
	if err != nil {
		s.writeStreamError(w, err)
		return
	}

	rel := filepath.ToSlash(filepath.Join("snapshots", serial, fmt.Sprintf("snap_%d_cam%d.jpg", time.Now().UnixNano(), req.Camera)))
	full := filepath.Join(s.clipsRoot, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "could not create storage dir"})
		return
	}
	if err := os.WriteFile(full, img, 0o644); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "could not write snapshot file"})
		return
	}
	id, err := s.data.CreateSnapshot(ctx, serial, req.Camera, kind, source, capturedUTC, devicePath, rel, int64(len(img)))
	if err != nil {
		_ = os.Remove(full)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id, "file_size": len(img), "storage_path": rel})
}

// GET /api/snapshots?serial=&limit=&offset= — list saved snapshots, newest first.
func (s *Server) handleListSnapshots(w http.ResponseWriter, r *http.Request) {
	if !s.dataReady(w) {
		return
	}
	limit, offset := pageParams(r)
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	snaps, err := s.data.ListSnapshots(ctx, r.URL.Query().Get("serial"), limit, offset)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"snapshots": snaps})
}

// GET /api/snapshots/{id}/download — stream a saved snapshot's JPEG.
func (s *Server) handleSnapshotDownload(w http.ResponseWriter, r *http.Request) {
	if !s.dataReady(w) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid snapshot id"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	snap, err := s.data.GetSnapshot(ctx, id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "snapshot not found"})
		return
	}
	if s.clipsRoot == "" {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "snapshot storage not configured"})
		return
	}
	rel, _ := snap["storage_path"].(string)
	full := filepath.Join(s.clipsRoot, filepath.FromSlash(rel))
	if relCheck, err := filepath.Rel(s.clipsRoot, full); err != nil || strings.HasPrefix(relCheck, "..") {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid snapshot path"})
		return
	}
	if _, err := os.Stat(full); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "snapshot file missing"})
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filepath.Base(full)))
	http.ServeFile(w, r, full)
}

// DELETE /api/snapshots/{id} — remove a saved snapshot row and its file.
func (s *Server) handleDeleteSnapshot(w http.ResponseWriter, r *http.Request) {
	if !s.dataReady(w) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid snapshot id"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	storagePath, err := s.data.DeleteSnapshot(ctx, id)
	if err != nil {
		if err.Error() == notFoundMsg {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "snapshot not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if storagePath != "" && s.clipsRoot != "" {
		_ = os.Remove(filepath.Join(s.clipsRoot, filepath.FromSlash(storagePath)))
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// POST /api/units/{serial}/clips — ask the device to upload a recorded clip. The
// .mp4 arrives asynchronously; the response carries the clip id to poll.
func (s *Server) handleClipRequest(w http.ResponseWriter, r *http.Request) {
	serial := r.PathValue("serial")
	if s.hub == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "unit not connected"})
		return
	}
	vc, ok := s.hub.Video(serial)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "unit not connected or video unavailable"})
		return
	}
	var req gateway.ClipRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON body"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	info, err := vc.RequestClip(ctx, req)
	if err != nil {
		s.writeStreamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "clip_id": info.ClipID, "session_id": info.SessionID, "status": info.Status,
	})
}

// GET /api/clips?serial=&limit=&offset= — list recorded clips, newest first.
func (s *Server) handleListClips(w http.ResponseWriter, r *http.Request) {
	if !s.dataReady(w) {
		return
	}
	limit, offset := pageParams(r)
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	clips, err := s.data.ListClips(ctx, r.URL.Query().Get("serial"), limit, offset)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"clips": clips})
}

// GET /api/clips/{id} — one clip's metadata/status.
func (s *Server) handleGetClip(w http.ResponseWriter, r *http.Request) {
	clip, ok := s.lookupClip(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, clip)
}

// GET /api/clips/{id}/download — stream the stored .mp4.
func (s *Server) handleClipDownload(w http.ResponseWriter, r *http.Request) {
	clip, ok := s.lookupClip(w, r)
	if !ok {
		return
	}
	if status, _ := clip["status"].(string); status != "ready" {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "clip not ready", "status": clip["status"]})
		return
	}
	if s.clipsRoot == "" {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "clip storage not configured"})
		return
	}
	rel, _ := clip["storage_path"].(string)
	full := filepath.Join(s.clipsRoot, filepath.FromSlash(rel))
	if relCheck, err := filepath.Rel(s.clipsRoot, full); err != nil || strings.HasPrefix(relCheck, "..") {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid clip path"})
		return
	}
	if _, err := os.Stat(full); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "clip file missing"})
		return
	}
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filepath.Base(full)))
	http.ServeFile(w, r, full)
}

// DELETE /api/clips/{id} — remove the record and its file.
func (s *Server) handleDeleteClip(w http.ResponseWriter, r *http.Request) {
	if !s.dataReady(w) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid clip id"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	storagePath, err := s.data.DeleteClip(ctx, id)
	if err != nil {
		if err.Error() == notFoundMsg {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "clip not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if storagePath != "" && s.clipsRoot != "" {
		_ = os.Remove(filepath.Join(s.clipsRoot, filepath.FromSlash(storagePath)))
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// lookupClip parses {id}, fetches the clip, and writes the appropriate error
// response (returning ok=false) when missing.
func (s *Server) lookupClip(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
	if !s.dataReady(w) {
		return nil, false
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid clip id"})
		return nil, false
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	clip, err := s.data.GetClip(ctx, id)
	if err != nil {
		if err.Error() == notFoundMsg {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "clip not found"})
			return nil, false
		}
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return nil, false
	}
	return clip, true
}

// GET /api/users — list accounts (never password hashes).
func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	if !s.dataReady(w) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	rows, err := s.data.ListUsers(ctx)
	if err != nil {
		s.dataError(w, "list_users", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": rows})
}

// POST /api/users — create an account.
func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	if !s.dataReady(w) {
		return
	}
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON body"})
		return
	}
	if !validEmail(body.Email) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "a valid email is required"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := s.data.CreateUser(ctx, body.Email, body.Password); err != nil {
		s.writeUserError(w, "create_user", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "email": strings.TrimSpace(body.Email)})
}

// PUT /api/users/{id} — toggle active and/or reset password.
func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	if !s.dataReady(w) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid id"})
		return
	}
	var body struct {
		IsActive *bool   `json:"is_active"`
		Password *string `json:"password"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON body"})
		return
	}
	if body.IsActive == nil && body.Password == nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "nothing to update"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if body.Password != nil {
		if err := s.data.SetUserPassword(ctx, id, *body.Password); err != nil {
			s.writeUserError(w, "set_user_password", err)
			return
		}
	}
	if body.IsActive != nil {
		if err := s.data.SetUserActive(ctx, id, *body.IsActive); err != nil {
			s.writeUserError(w, "set_user_active", err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id})
}

// DELETE /api/users/{id} — remove an account.
func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	if !s.dataReady(w) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid id"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := s.data.DeleteUser(ctx, id); err != nil {
		s.writeUserError(w, "delete_user", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": id})
}

// writeUserError maps user-store errors to status codes.
func (s *Server) writeUserError(w http.ResponseWriter, event string, err error) {
	msg := err.Error()
	switch {
	case msg == notFoundMsg:
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "user not found"})
	case strings.Contains(msg, "already exists"):
		writeJSON(w, http.StatusConflict, map[string]any{"error": msg})
	case strings.Contains(msg, "last active user"):
		writeJSON(w, http.StatusConflict, map[string]any{"error": msg})
	case strings.Contains(msg, "required") || strings.Contains(msg, "at least") || strings.Contains(msg, "invalid"):
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": msg})
	default:
		s.dataError(w, event, err)
	}
}

// validEmail is a light sanity check (not full RFC 5322): non-empty, one @, a dot
// in the domain. Real validation is delivery; this just blocks obvious mistakes.
func validEmail(email string) bool {
	email = strings.TrimSpace(email)
	at := strings.IndexByte(email, '@')
	if at <= 0 || at == len(email)-1 {
		return false
	}
	return strings.Contains(email[at+1:], ".")
}

// GET /api/api-keys — list API keys (metadata only; never the key or hash).
func (s *Server) handleListAPIKeys(w http.ResponseWriter, r *http.Request) {
	if !s.dataReady(w) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	rows, err := s.data.ListAPIKeyMeta(ctx)
	if err != nil {
		s.dataError(w, "list_api_keys", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"api_keys": rows})
}

// POST /api/api-keys — mint a key. The plaintext is returned ONCE here and never
// stored (only its sha256 hash is). Callers must save it immediately.
func (s *Server) handleCreateAPIKey(w http.ResponseWriter, r *http.Request) {
	if !s.dataReady(w) {
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON body"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	key, err := s.data.CreateAPIKey(ctx, strings.TrimSpace(body.Name))
	if err != nil {
		s.dataError(w, "create_api_key", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"key": key, "name": strings.TrimSpace(body.Name)})
}

// DELETE /api/api-keys/{prefix} — revoke (deactivate) the key(s) with that prefix.
func (s *Server) handleRevokeAPIKey(w http.ResponseWriter, r *http.Request) {
	if !s.dataReady(w) {
		return
	}
	prefix := r.PathValue("prefix")
	if strings.TrimSpace(prefix) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "prefix is required"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	n, err := s.data.RevokeAPIKey(ctx, prefix)
	if err != nil {
		s.dataError(w, "revoke_api_key", err)
		return
	}
	if n == 0 {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no active key with that prefix"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"revoked": n})
}

// GET /api/units — list currently-connected devices.
func (s *Server) handleListUnits(w http.ResponseWriter, _ *http.Request) {
	if s.hub == nil {
		writeJSON(w, http.StatusOK, map[string]any{"units": []any{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"units": s.hub.List()})
}

// GET /api/units/{serial} — one connected device's info.
func (s *Server) handleGetUnit(w http.ResponseWriter, r *http.Request) {
	serial := r.PathValue("serial")
	if s.hub == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "unit not connected"})
		return
	}
	info, ok := s.hub.Get(serial)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "unit not connected"})
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// GET /api/units/{serial}/status — the connected device's live status detail
// (connection/server + mobile network/4G, modules, storage, IO, GPS, …).
func (s *Server) handleUnitStatus(w http.ResponseWriter, r *http.Request) {
	serial := r.PathValue("serial")
	if s.hub == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "unit not connected"})
		return
	}
	info, ok := s.hub.Get(serial)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "unit not connected"})
		return
	}
	telemetry, _ := s.hub.Status(serial)
	writeJSON(w, http.StatusOK, map[string]any{
		"serial":     serial,
		"connection": info,
		"telemetry":  telemetry,
	})
}

// defaultConfigModules is the Phase-1 set read when ?modules= is omitted.
var defaultConfigModules = []string{"VERSIONINFO", "JTBASE", "WIFI", "DIALUP", "SERVER"}

// GET /api/units/{serial}/config?modules=WIFI,DIALUP,… — read the unit's config.
func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	cc, ok := s.unitConfig(w, r.PathValue("serial"))
	if !ok {
		return
	}
	modules := defaultConfigModules
	if v := strings.TrimSpace(r.URL.Query().Get("modules")); v != "" {
		modules = []string{}
		for _, m := range strings.Split(v, ",") {
			if m = strings.TrimSpace(m); m != "" {
				modules = append(modules, m)
			}
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	sc, err := cc.RequestConfig(ctx, modules)
	if err != nil {
		s.writeStreamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sc": sc})
}

// PUT /api/units/{serial}/config — write changed fields, then re-read them.
// Body: {"sc": {"WIFI": {"SSID": "x"}}} — send ONLY the fields being changed.
func (s *Server) handleUpdateConfig(w http.ResponseWriter, r *http.Request) {
	cc, ok := s.unitConfig(w, r.PathValue("serial"))
	if !ok {
		return
	}
	var body struct {
		Sc map[string]any `json:"sc"`
	}
	if err := decodeJSON(w, r, &body); err != nil || len(body.Sc) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "body must be {\"sc\": {SEGMENT: {field: value}}}"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	if err := cc.UpdateConfig(ctx, body.Sc); err != nil {
		s.writeStreamError(w, err)
		return
	}
	// Re-read the segments we just wrote so the UI reflects the device truth.
	modules := make([]string, 0, len(body.Sc))
	for k := range body.Sc {
		modules = append(modules, k)
	}
	sc, err := cc.RequestConfig(ctx, modules)
	if err != nil {
		// The write succeeded; only the read-back failed.
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "sc": map[string]any{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "sc": sc})
}

// unitConfig resolves the config controller for a connected unit, writing the
// appropriate error response when unavailable.
func (s *Server) unitConfig(w http.ResponseWriter, serial string) (gateway.ConfigController, bool) {
	if s.hub == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "unit not connected"})
		return nil, false
	}
	cc, ok := s.hub.Config(serial)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "unit not connected or config unavailable"})
		return nil, false
	}
	return cc, true
}

// POST /api/units/{serial}/commands — send a control command to a device.
func (s *Server) handleCommand(w http.ResponseWriter, r *http.Request) {
	serial := r.PathValue("serial")
	if s.hub == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "unit not connected"})
		return
	}

	var cmd gateway.Command
	if err := decodeJSON(w, r, &cmd); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON body"})
		return
	}
	if strings.TrimSpace(cmd.Type) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": `"type" is required`})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()

	result, err := s.hub.Send(ctx, serial, cmd)
	if err != nil {
		s.writeCommandError(w, serial, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// GET /api/devices — approved device registry.
func (s *Server) handleListDevices(w http.ResponseWriter, r *http.Request) {
	if !s.dataReady(w) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	rows, err := s.data.ListDevices(ctx)
	if err != nil {
		s.dataError(w, "list_devices", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": rows})
}

// GET /api/devices/pending — quarantined devices awaiting approval.
func (s *Server) handleListPending(w http.ResponseWriter, r *http.Request) {
	if !s.dataReady(w) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	rows, err := s.data.ListPendingDevices(ctx)
	if err != nil {
		s.dataError(w, "list_pending", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": rows})
}

// POST /api/devices/{serial}/approve — whitelist a serial.
func (s *Server) handleApproveDevice(w http.ResponseWriter, r *http.Request) {
	if !s.dataReady(w) {
		return
	}
	serial := r.PathValue("serial")
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	// The fallback protocol is used only when the pending device has no known
	// protocol guess; an explicit ?unit= overrides the default.
	if err := s.data.ApproveDevice(ctx, serial, s.unitOrDefault(r.URL.Query().Get("unit"))); err != nil {
		s.dataError(w, "approve_device", err)
		return
	}
	s.log.Info(map[string]any{"event": "device_approved", "serial": serial})
	writeJSON(w, http.StatusOK, map[string]any{"approved": serial})
}

// POST /api/devices/{serial}/reject — drop a serial from quarantine.
func (s *Server) handleRejectDevice(w http.ResponseWriter, r *http.Request) {
	if !s.dataReady(w) {
		return
	}
	serial := r.PathValue("serial")
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := s.data.RejectDevice(ctx, serial); err != nil {
		s.dataError(w, "reject_device", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rejected": serial})
}

// DELETE /api/devices/{serial} — remove a serial from the approved registry.
func (s *Server) handleDeleteDevice(w http.ResponseWriter, r *http.Request) {
	if !s.dataReady(w) {
		return
	}
	serial := r.PathValue("serial")
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := s.data.DeleteDevice(ctx, serial); err != nil {
		s.dataError(w, "delete_device", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": serial})
}

// GET /api/mappings?unit=howen&model= — editable event mappings for a unit and
// model (empty model = the unit-wide default).
func (s *Server) handleListMappings(w http.ResponseWriter, r *http.Request) {
	if !s.dataReady(w) {
		return
	}
	unit, ok := s.requireUnit(w, r)
	if !ok {
		return
	}
	model := strings.TrimSpace(r.URL.Query().Get("model"))
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	rows, err := s.data.ListEventMappings(ctx, unit, model)
	if err != nil {
		s.dataError(w, "list_mappings", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"unit": unit, "model": model, "mappings": rows})
}

// GET /api/mappings/models?unit=howen — the distinct non-empty models that have
// their own mapping table for a unit.
func (s *Server) handleListMappingModels(w http.ResponseWriter, r *http.Request) {
	if !s.dataReady(w) {
		return
	}
	unit, ok := s.requireUnit(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	models, err := s.data.ListEventMappingModels(ctx, unit)
	if err != nil {
		s.dataError(w, "list_mapping_models", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"unit": unit, "models": models})
}

// POST /api/mappings/copy {"unit":"howen","from_model":"","to_model":"Hero-ME40"}
// — seed a model's table by copying every row from another model (default ”).
func (s *Server) handleCopyMappings(w http.ResponseWriter, r *http.Request) {
	if !s.dataReady(w) {
		return
	}
	var body struct {
		Unit      string `json:"unit"`
		FromModel string `json:"from_model"`
		ToModel   string `json:"to_model"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON body"})
		return
	}
	unit := s.unitOrDefault(body.Unit)
	if !s.unitNames()[unit] {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "unknown unit: " + unit})
		return
	}
	if strings.TrimSpace(body.ToModel) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "to_model is required"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := s.data.CopyEventMappings(ctx, unit, strings.TrimSpace(body.FromModel), strings.TrimSpace(body.ToModel)); err != nil {
		if isValidationErr(err) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		s.dataError(w, "copy_mappings", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "unit": unit, "to_model": body.ToModel})
}

// PUT /api/mappings — create or update one mapping row.
func (s *Server) handleUpsertMapping(w http.ResponseWriter, r *http.Request) {
	if !s.dataReady(w) {
		return
	}
	var body struct {
		Unit        string `json:"unit"`
		Model       string `json:"model"`
		MapType     string `json:"map_type"`
		Code        int    `json:"code"`
		EventCode   string `json:"event_code"`
		Description string `json:"description"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON body"})
		return
	}
	unit := s.unitOrDefault(body.Unit)
	if !s.unitNames()[unit] {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "unknown unit: " + unit})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	entry := mapping.Entry{Model: strings.TrimSpace(body.Model), MapType: body.MapType, Code: body.Code, EventCode: body.EventCode, Description: body.Description}
	if err := s.data.UpsertEventMapping(ctx, unit, entry); err != nil {
		if isValidationErr(err) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		s.dataError(w, "upsert_mapping", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "unit": unit, "map_type": body.MapType, "code": body.Code})
}

// DELETE /api/mappings?unit=howen&map_type=dms_adas&code=34 — remove a mapping.
func (s *Server) handleDeleteMapping(w http.ResponseWriter, r *http.Request) {
	if !s.dataReady(w) {
		return
	}
	unit, ok := s.requireUnit(w, r)
	if !ok {
		return
	}
	model := strings.TrimSpace(r.URL.Query().Get("model"))
	mapType := strings.TrimSpace(r.URL.Query().Get("map_type"))
	codeStr := strings.TrimSpace(r.URL.Query().Get("code"))
	if mapType == "" || codeStr == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "map_type and code are required"})
		return
	}
	code, err := strconv.Atoi(codeStr)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "code must be an integer"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := s.data.DeleteEventMapping(ctx, unit, model, mapType, code); err != nil {
		if err.Error() == notFoundMsg {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "mapping not found"})
			return
		}
		s.dataError(w, "delete_mapping", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": map[string]any{"unit": unit, "model": model, "map_type": mapType, "code": code}})
}

// GET /api/settings — all editable server settings.
func (s *Server) handleListSettings(w http.ResponseWriter, r *http.Request) {
	if !s.dataReady(w) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	rows, err := s.data.ListSettings(ctx)
	if err != nil {
		s.dataError(w, "list_settings", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": rows})
}

// PUT /api/settings — upsert one setting. The webhook_url value is validated as
// an http(s) URL (empty disables the telemetry webhook).
func (s *Server) handleSetSetting(w http.ResponseWriter, r *http.Request) {
	if !s.dataReady(w) {
		return
	}
	var body struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON body"})
		return
	}
	key := strings.TrimSpace(body.Key)
	if key == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": `"key" is required`})
		return
	}
	if key == settingWebhookURL && strings.TrimSpace(body.Value) != "" && !validHTTPURL(body.Value) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "webhook_url must be a valid http(s) URL"})
		return
	}
	if key == settingDevicePort {
		if p, err := strconv.Atoi(strings.TrimSpace(body.Value)); err != nil || p < 1 || p > 65535 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "device_port must be a whole number between 1 and 65535"})
			return
		}
	}
	if key == settingDeviceRejectUnknown && !validBool(body.Value) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "device_reject_unknown must be true or false"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := s.data.SetSetting(ctx, key, body.Value); err != nil {
		if isValidationErr(err) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		s.dataError(w, "set_setting", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "key": key})
}

// GET /api/unit-types/{unit}/settings/schema — a unit's editable settings schema
// (declared by the unit). Drives the unit's settings screen in the admin panel.
func (s *Server) handleUnitSettingsSchema(w http.ResponseWriter, r *http.Request) {
	u, ok := s.unitInfo(r.PathValue("unit"))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "unknown unit"})
		return
	}
	schema := u.Schema
	if schema == nil {
		schema = []gateway.SettingField{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"unit": u.Name, "schema": schema})
}

// GET /api/unit-types/{unit}/settings — a unit's current stored setting values.
func (s *Server) handleListUnitSettings(w http.ResponseWriter, r *http.Request) {
	if !s.dataReady(w) {
		return
	}
	unit := r.PathValue("unit")
	if !s.unitNames()[unit] {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "unknown unit"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	rows, err := s.data.ListUnitSettings(ctx, unit)
	if err != nil {
		s.dataError(w, "list_unit_settings", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"unit": unit, "settings": rows})
}

// PUT /api/unit-types/{unit}/settings — upsert one of a unit's settings. The key
// must be declared in the unit's schema; the value is validated against the
// declared field type. Applies to the running gateway instantly via NOTIFY.
func (s *Server) handleSetUnitSetting(w http.ResponseWriter, r *http.Request) {
	if !s.dataReady(w) {
		return
	}
	u, ok := s.unitInfo(r.PathValue("unit"))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "unknown unit"})
		return
	}
	var body struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON body"})
		return
	}
	key := strings.TrimSpace(body.Key)
	field, known := unitSettingField(u.Schema, key)
	if !known {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "unknown setting key for unit " + u.Name})
		return
	}
	if msg := validateSettingValue(field, body.Value); msg != "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": msg})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := s.data.SetUnitSetting(ctx, u.Name, key, body.Value); err != nil {
		s.dataError(w, "set_unit_setting", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "unit": u.Name, "key": key})
}

// GET /api/unit-types/{unit}/ports — a unit's configured and active listener
// ports. device_port is the TCP port devices dial; for video units media_port is
// the device-side media port. The *_active values are what the gateway bound on
// startup (configured ≠ active ⇒ a restart is pending).
func (s *Server) handleGetPorts(w http.ResponseWriter, r *http.Request) {
	u, ok := s.unitInfo(r.PathValue("unit"))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "unknown unit"})
		return
	}
	if !s.dataReady(w) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	rows, err := s.data.ListSettings(ctx)
	if err != nil {
		s.dataError(w, "list_ports", err)
		return
	}
	m := map[string]string{}
	for _, row := range rows {
		k, _ := row["key"].(string)
		v, _ := row["value"].(string)
		m[k] = v
	}
	resp := map[string]any{
		"unit":               u.Name,
		"has_video":          u.Caps.HasVideo,
		"device_port":        m[DevicePortKey(u.Name)],
		"device_port_active": m[DevicePortActiveKey(u.Name)],
	}
	if u.Caps.HasVideo {
		resp["media_port"] = m[MediaPortKey(u.Name)]
		resp["media_port_active"] = m[MediaPortActiveKey(u.Name)]
	}
	writeJSON(w, http.StatusOK, resp)
}

// PUT /api/unit-types/{unit}/ports {"device_port":33000,"media_port":33001} — set
// a unit's ports. Takes effect on the next gateway restart (and, in Docker, the
// published port mapping must be updated to match). media_port is rejected for a
// unit without video.
func (s *Server) handleSetPorts(w http.ResponseWriter, r *http.Request) {
	u, ok := s.unitInfo(r.PathValue("unit"))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "unknown unit"})
		return
	}
	if !s.dataReady(w) {
		return
	}
	var body struct {
		DevicePort *int `json:"device_port"`
		MediaPort  *int `json:"media_port"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON body"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if body.DevicePort != nil {
		if !validPort(*body.DevicePort) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "device_port must be between 1 and 65535"})
			return
		}
		if err := s.data.SetSetting(ctx, DevicePortKey(u.Name), itoa(*body.DevicePort)); err != nil {
			s.dataError(w, "set_device_port", err)
			return
		}
	}
	if body.MediaPort != nil {
		if !u.Caps.HasVideo {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "unit has no video — media_port not applicable"})
			return
		}
		if !validPort(*body.MediaPort) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "media_port must be between 1 and 65535"})
			return
		}
		if err := s.data.SetSetting(ctx, MediaPortKey(u.Name), itoa(*body.MediaPort)); err != nil {
			s.dataError(w, "set_media_port", err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "unit": u.Name})
}

func validPort(p int) bool { return p >= 1 && p <= 65535 }

// PUT /api/unit-types/{unit}/capabilities {"video":false,"config":true,...} — set
// a unit's capability disable-toggles. Only features the unit SUPPORTS may be
// enabled; enabling an unsupported feature is rejected. Applies live (the admin
// re-reads /api/gateway/info).
func (s *Server) handleSetCapabilities(w http.ResponseWriter, r *http.Request) {
	u, ok := s.unitInfo(r.PathValue("unit"))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "unknown unit"})
		return
	}
	if !s.dataReady(w) {
		return
	}
	var body struct {
		Video    *bool `json:"video"`
		Commands *bool `json:"commands"`
		Config   *bool `json:"config"`
		Status   *bool `json:"status"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON body"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	fields := []struct {
		feature   string
		val       *bool
		supported bool
	}{
		{"video", body.Video, u.Caps.HasVideo},
		{"commands", body.Commands, u.Caps.HasCommands},
		{"config", body.Config, u.Caps.HasConfig},
		{"status", body.Status, u.Caps.HasStatus},
	}
	for _, f := range fields {
		if f.val == nil {
			continue
		}
		if *f.val && !f.supported {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": u.Name + " does not support " + f.feature})
			return
		}
		if err := s.data.SetSetting(ctx, capKey(f.feature, u.Name), strconv.FormatBool(*f.val)); err != nil {
			s.dataError(w, "set_capability", err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "unit": u.Name})
}

// unitSettingField finds a schema field by key.
func unitSettingField(schema []gateway.SettingField, key string) (gateway.SettingField, bool) {
	for _, f := range schema {
		if f.Key == key {
			return f, true
		}
	}
	return gateway.SettingField{}, false
}

// validateSettingValue checks a value against a field's declared type, returning
// an error message ("" when valid).
func validateSettingValue(f gateway.SettingField, value string) string {
	v := strings.TrimSpace(value)
	switch f.Type {
	case "number":
		if v == "" {
			return f.Key + " must be a number"
		}
		if _, err := strconv.ParseFloat(v, 64); err != nil {
			return f.Key + " must be a number"
		}
	case "bool":
		if !validBool(v) {
			return f.Key + " must be true or false"
		}
	case "select":
		for _, opt := range f.Options {
			if v == opt {
				return ""
			}
		}
		return f.Key + " must be one of: " + strings.Join(f.Options, ", ")
	}
	return ""
}

// validBool reports whether v is a recognizable boolean string.
func validBool(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "false", "1", "0", "yes", "no", "on", "off":
		return true
	}
	return false
}

// validHTTPURL reports whether v is an absolute http(s) URL with a host.
func validHTTPURL(v string) bool {
	u, err := url.Parse(strings.TrimSpace(v))
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// GET /api/webhooks — all telemetry webhooks.
func (s *Server) handleListWebhooks(w http.ResponseWriter, r *http.Request) {
	if !s.dataReady(w) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	rows, err := s.data.ListWebhooks(ctx)
	if err != nil {
		s.dataError(w, "list_webhooks", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"webhooks": rows})
}

// webhookBody is the create/update payload. Enabled defaults to true when omitted.
type webhookBody struct {
	Name    string `json:"name"`
	URL     string `json:"url"`
	Enabled *bool  `json:"is_enabled"`
}

func (b webhookBody) enabled() bool { return b.Enabled == nil || *b.Enabled }

// POST /api/webhooks — add a telemetry webhook.
func (s *Server) handleCreateWebhook(w http.ResponseWriter, r *http.Request) {
	if !s.dataReady(w) {
		return
	}
	var body webhookBody
	if err := decodeJSON(w, r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON body"})
		return
	}
	if !validHTTPURL(body.URL) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "url must be a valid http(s) URL"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	id, err := s.data.CreateWebhook(ctx, strings.TrimSpace(body.Name), strings.TrimSpace(body.URL), body.enabled())
	if err != nil {
		if isValidationErr(err) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		s.dataError(w, "create_webhook", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id})
}

// PUT /api/webhooks/{id} — update name, URL, and enabled flag.
func (s *Server) handleUpdateWebhook(w http.ResponseWriter, r *http.Request) {
	if !s.dataReady(w) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid id"})
		return
	}
	var body webhookBody
	if err := decodeJSON(w, r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON body"})
		return
	}
	if !validHTTPURL(body.URL) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "url must be a valid http(s) URL"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := s.data.UpdateWebhook(ctx, id, strings.TrimSpace(body.Name), strings.TrimSpace(body.URL), body.enabled()); err != nil {
		if err.Error() == notFoundMsg {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "webhook not found"})
			return
		}
		if isValidationErr(err) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		s.dataError(w, "update_webhook", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id})
}

// DELETE /api/webhooks/{id} — remove a webhook.
func (s *Server) handleDeleteWebhook(w http.ResponseWriter, r *http.Request) {
	if !s.dataReady(w) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid id"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := s.data.DeleteWebhook(ctx, id); err != nil {
		if err.Error() == notFoundMsg {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "webhook not found"})
			return
		}
		s.dataError(w, "delete_webhook", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": id})
}

// GET /api/event-codes — the canonical ACM Standard Event Codes picklist.
func (s *Server) handleEventCodes(w http.ResponseWriter, r *http.Request) {
	if !s.dataReady(w) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	rows, err := s.data.ListStandardEventCodes(ctx)
	if err != nil {
		s.dataError(w, "list_event_codes", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"event_codes": rows})
}

// POST /api/event-codes {"code":"COLLISION:SEVERE","category":"Collision","notes":""}
// — add (or refresh) a custom event code in the picklist so it's selectable in the
// mapping editor.
func (s *Server) handleAddEventCode(w http.ResponseWriter, r *http.Request) {
	if !s.dataReady(w) {
		return
	}
	var body struct {
		Code     string `json:"code"`
		Category string `json:"category"`
		Notes    string `json:"notes"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON body"})
		return
	}
	code := strings.TrimSpace(body.Code)
	if code == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": `"code" is required`})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := s.data.AddStandardEventCode(ctx, code, strings.TrimSpace(body.Category), strings.TrimSpace(body.Notes)); err != nil {
		s.dataError(w, "add_event_code", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "code": code})
}

// GET /api/logs — recent gateway/system errors.
func (s *Server) handleGatewayErrors(w http.ResponseWriter, r *http.Request) {
	if !s.dataReady(w) {
		return
	}
	limit, offset := pageParams(r)
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	rows, err := s.data.ListGatewayErrors(ctx, limit, offset)
	if err != nil {
		s.dataError(w, "list_logs", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"logs": rows, "limit": limit, "offset": offset})
}

// GET /api/device-errors — recent device-reported errors.
func (s *Server) handleDeviceErrors(w http.ResponseWriter, r *http.Request) {
	if !s.dataReady(w) {
		return
	}
	limit, offset := pageParams(r)
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	rows, err := s.data.ListDeviceErrors(ctx, limit, offset)
	if err != nil {
		s.dataError(w, "list_device_errors", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"errors": rows, "limit": limit, "offset": offset})
}

// GET /api/logs/live?after=<seq>&level=<info|debug|error>&unit=<>&q=<>&limit=<> —
// the in-memory tail of the gateway's live log stream. Poll with the returned
// cursor as the next `after` to follow it. Works without a database.
func (s *Server) handleLiveLogs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	after, _ := strconv.ParseUint(strings.TrimSpace(q.Get("after")), 10, 64)
	level := strings.TrimSpace(q.Get("level"))
	if level == "" {
		level = "info"
	}
	limit := 500
	if v := strings.TrimSpace(q.Get("limit")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 2000 {
			limit = n
		}
	}
	entries, cursor := s.log.LiveSince(after, level, q.Get("unit"), q.Get("q"), limit)
	writeJSON(w, http.StatusOK, map[string]any{
		"entries":       entries,
		"cursor":        cursor,
		"capture_level": s.log.CaptureLevel(),
	})
}

// GET /api/logs/level — the live-log buffer's current capture verbosity.
func (s *Server) handleGetLogLevel(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"level": s.log.CaptureLevel()})
}

// PUT /api/logs/level {"level":"debug|info|error"} — set the live-log capture
// verbosity at runtime (e.g. flip to debug to watch per-frame device activity).
// Does NOT change stdout/container-log verbosity.
func (s *Server) handleSetLogLevel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Level string `json:"level"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON body"})
		return
	}
	lvl := strings.ToLower(strings.TrimSpace(body.Level))
	switch lvl {
	case "debug", "info", "error":
	default:
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "level must be debug, info, or error"})
		return
	}
	s.log.SetCaptureLevel(lvl)
	s.log.Info(map[string]any{"event": "live_log_level_changed", "level": lvl})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "level": lvl})
}

func (s *Server) writeCommandError(w http.ResponseWriter, serial string, err error) {
	switch {
	case errors.Is(err, gateway.ErrNotConnected):
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "unit not connected"})
	case errors.Is(err, gateway.ErrUnsupportedCommand):
		body := map[string]any{"error": err.Error()}
		if info, ok := s.hub.Get(serial); ok {
			body["supported_commands"] = info.Commands
		}
		writeJSON(w, http.StatusBadRequest, body)
	case errors.Is(err, gateway.ErrInvalidCommand):
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
	case errors.Is(err, gateway.ErrCommandTimeout):
		writeJSON(w, http.StatusGatewayTimeout, map[string]any{"error": "device did not respond in time"})
	default:
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
	}
}

// dataReady reports whether the data store is configured, writing 503 if not.
func (s *Server) dataReady(w http.ResponseWriter) bool {
	if s.data == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "database not configured"})
		return false
	}
	return true
}

func (s *Server) dataError(w http.ResponseWriter, event string, err error) {
	s.log.Error(map[string]any{"event": event + "_error", "error": err.Error()})
	writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal error"})
}

// unitOrDefault returns an explicit unit value, falling back to the back-compat
// default unit when blank.
func (s *Server) unitOrDefault(unit string) string {
	if u := strings.TrimSpace(unit); u != "" {
		return u
	}
	return s.defaultUnit
}

// requireUnit resolves the unit for a unit-scoped endpoint from the `?unit=` query
// parameter. When only one unit is hosted it defaults to that unit; when several
// are hosted the parameter is mandatory (writes 400 and returns ok=false). A
// supplied unit is validated against the hosted set.
func (s *Server) requireUnit(w http.ResponseWriter, r *http.Request) (string, bool) {
	unit := strings.TrimSpace(r.URL.Query().Get("unit"))
	if unit == "" {
		if len(s.units) == 1 {
			return s.defaultUnit, true
		}
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "unit query parameter is required"})
		return "", false
	}
	if !s.unitNames()[unit] {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "unknown unit: " + unit})
		return "", false
	}
	return unit, true
}

// requireAPIKey enforces a valid `Authorization: Bearer <key>` on the request.
func (s *Server) requireAPIKey(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := bearerToken(r)
		// The admin panel's internal service token is accepted before (and
		// independently of) the DB key store, so the panel works on first run.
		if s.internalToken != "" && key != "" &&
			subtle.ConstantTimeCompare([]byte(key), []byte(s.internalToken)) == 1 {
			next.ServeHTTP(w, r)
			return
		}
		if s.verifier == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "api auth unavailable (no key store)"})
			return
		}
		if key == "" {
			unauthorized(w)
			return
		}
		ok, err := s.verifier.VerifyAPIKey(r.Context(), key)
		if err != nil {
			s.log.Error(map[string]any{"event": "apikey_verify_error", "error": err.Error()})
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "auth error"})
			return
		}
		if !ok {
			unauthorized(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	const p = "Bearer "
	if len(h) > len(p) && strings.EqualFold(h[:len(p)], p) {
		return strings.TrimSpace(h[len(p):])
	}
	return ""
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", "Bearer")
	writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid or missing API key"})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) error {
	return json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(v)
}

// pageParams reads ?limit= and ?offset= (defaults handled by the store).
func pageParams(r *http.Request) (limit, offset int) {
	limit, _ = strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ = strconv.Atoi(r.URL.Query().Get("offset"))
	return limit, offset
}

// isValidationErr reports whether a store error is a user-input problem (400)
// rather than an internal failure (500). The store returns plain errors.New for
// validation, so match on the known messages.
func isValidationErr(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "required") || strings.Contains(msg, "invalid")
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

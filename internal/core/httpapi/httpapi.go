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
	"time"

	"github.com/dfm/device-gateway/internal/core/flow"
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

	ListEventMappings(ctx context.Context, unit string) ([]map[string]any, error)
	UpsertEventMapping(ctx context.Context, unit string, e mapping.Entry) error
	DeleteEventMapping(ctx context.Context, unit, mapType string, code int) error

	ListWorkflows(ctx context.Context, unit string) ([]map[string]any, error)
	GetWorkflow(ctx context.Context, unit, model string) (map[string]any, error)
	UpsertWorkflow(ctx context.Context, unit, model, name string, graph json.RawMessage, isActive bool) error
	DeleteWorkflow(ctx context.Context, unit, model string) error

	ListGatewayErrors(ctx context.Context, limit, offset int) ([]map[string]any, error)
	ListDeviceErrors(ctx context.Context, limit, offset int) ([]map[string]any, error)

	ListStandardEventCodes(ctx context.Context) ([]map[string]any, error)

	ListSettings(ctx context.Context) ([]map[string]any, error)
	SetSetting(ctx context.Context, key, value string) error

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

// Server is the HTTP API server.
type Server struct {
	addr          string
	unit          string
	log           *logging.Logger
	verifier      KeyVerifier
	data          DataStore
	hub           *gateway.Hub
	internalToken string
	hlsRoot       string
	clipsRoot     string
	srv           *http.Server
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

// New builds the API server. unit is this gateway's unit type (e.g. "howen"),
// used as the default for mapping endpoints. If verifier is nil, protected
// routes respond 503. If data is nil, the data-backed endpoints respond 503
// while connected-device endpoints still work via the hub.
func New(host string, port int, unit string, verifier KeyVerifier, data DataStore, hub *gateway.Hub, log *logging.Logger) *Server {
	s := &Server{
		addr:     net.JoinHostPort(host, itoa(port)),
		unit:     unit,
		log:      log.With("http"),
		verifier: verifier,
		data:     data,
		hub:      hub,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)

	// Protected route group. All inherit the API-key check.
	protected := map[string]http.HandlerFunc{
		"GET /api/ping": s.handlePing,

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
		"POST /api/units/{serial}/commands": s.handleCommand,

		// Live video.
		"POST /api/units/{serial}/stream/start": s.handleStreamStart,
		"POST /api/units/{serial}/stream/stop":  s.handleStreamStop,
		"GET /api/hls/":                         s.handleHLS,

		// Recorded clips (request a download, then poll status / download the .mp4).
		"POST /api/units/{serial}/clips": s.handleClipRequest,
		"GET /api/clips":                 s.handleListClips,
		"GET /api/clips/{id}":            s.handleGetClip,
		"GET /api/clips/{id}/download":   s.handleClipDownload,
		"DELETE /api/clips/{id}":         s.handleDeleteClip,

		// Device approval (registry, via the store).
		"GET /api/devices":                   s.handleListDevices,
		"GET /api/devices/pending":           s.handleListPending,
		"POST /api/devices/{serial}/approve": s.handleApproveDevice,
		"POST /api/devices/{serial}/reject":  s.handleRejectDevice,
		"DELETE /api/devices/{serial}":       s.handleDeleteDevice,

		// Server settings: editable event mappings (instant via NOTIFY).
		"GET /api/mappings":    s.handleListMappings,
		"PUT /api/mappings":    s.handleUpsertMapping,
		"DELETE /api/mappings": s.handleDeleteMapping,

		// Per-model visual mapping workflows (N8N-style).
		"GET /api/workflows":            s.handleListWorkflows,
		"POST /api/workflows/test":      s.handleTestWorkflow,
		"GET /api/workflows/{model}":    s.handleGetWorkflow,
		"PUT /api/workflows/{model}":    s.handleUpsertWorkflow,
		"DELETE /api/workflows/{model}": s.handleDeleteWorkflow,

		// Editable server settings.
		"GET /api/settings": s.handleListSettings,
		"PUT /api/settings": s.handleSetSetting,

		// Telemetry webhooks (the GPS/event data sinks).
		"GET /api/webhooks":         s.handleListWebhooks,
		"POST /api/webhooks":        s.handleCreateWebhook,
		"PUT /api/webhooks/{id}":    s.handleUpdateWebhook,
		"DELETE /api/webhooks/{id}": s.handleDeleteWebhook,

		// Reference data.
		"GET /api/event-codes": s.handleEventCodes,

		// Logs.
		"GET /api/logs":          s.handleGatewayErrors,
		"GET /api/device-errors": s.handleDeviceErrors,
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
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "unit": s.unit})
}

func (s *Server) handlePing(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
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

func (s *Server) writeStreamError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, gateway.ErrCommandTimeout):
		writeJSON(w, http.StatusGatewayTimeout, map[string]any{"error": "device did not start the stream in time"})
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
	case ".ts":
		w.Header().Set("Content-Type", "video/mp2t")
	}
	http.StripPrefix("/api/hls/", http.FileServer(http.Dir(s.hlsRoot))).ServeHTTP(w, r)
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
	if err := s.data.ApproveDevice(ctx, serial, s.unit); err != nil {
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

// GET /api/mappings?unit=howen — editable event mappings.
func (s *Server) handleListMappings(w http.ResponseWriter, r *http.Request) {
	if !s.dataReady(w) {
		return
	}
	unit := s.unitParam(r)
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	rows, err := s.data.ListEventMappings(ctx, unit)
	if err != nil {
		s.dataError(w, "list_mappings", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"unit": unit, "mappings": rows})
}

// PUT /api/mappings — create or update one mapping row.
func (s *Server) handleUpsertMapping(w http.ResponseWriter, r *http.Request) {
	if !s.dataReady(w) {
		return
	}
	var body struct {
		Unit        string `json:"unit"`
		MapType     string `json:"map_type"`
		Code        int    `json:"code"`
		EventCode   string `json:"event_code"`
		Description string `json:"description"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON body"})
		return
	}
	unit := body.Unit
	if strings.TrimSpace(unit) == "" {
		unit = s.unit
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	entry := mapping.Entry{MapType: body.MapType, Code: body.Code, EventCode: body.EventCode, Description: body.Description}
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
	unit := s.unitParam(r)
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
	if err := s.data.DeleteEventMapping(ctx, unit, mapType, code); err != nil {
		if err.Error() == notFoundMsg {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "mapping not found"})
			return
		}
		s.dataError(w, "delete_mapping", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": map[string]any{"unit": unit, "map_type": mapType, "code": code}})
}

// GET /api/workflows — per-model workflow summaries for this unit.
func (s *Server) handleListWorkflows(w http.ResponseWriter, r *http.Request) {
	if !s.dataReady(w) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	rows, err := s.data.ListWorkflows(ctx, s.unit)
	if err != nil {
		s.dataError(w, "list_workflows", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"unit": s.unit, "workflows": rows})
}

// GET /api/workflows/{model} — one model's full workflow (graph included).
func (s *Server) handleGetWorkflow(w http.ResponseWriter, r *http.Request) {
	if !s.dataReady(w) {
		return
	}
	model := r.PathValue("model")
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	wf, err := s.data.GetWorkflow(ctx, s.unit, model)
	if err != nil {
		if err.Error() == notFoundMsg {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "no workflow for this model"})
			return
		}
		s.dataError(w, "get_workflow", err)
		return
	}
	writeJSON(w, http.StatusOK, wf)
}

// PUT /api/workflows/{model} — create or replace a model's workflow. The store
// validates the graph; a structurally invalid graph returns 400.
func (s *Server) handleUpsertWorkflow(w http.ResponseWriter, r *http.Request) {
	if !s.dataReady(w) {
		return
	}
	model := r.PathValue("model")
	var body struct {
		Name     string          `json:"name"`
		Graph    json.RawMessage `json:"graph"`
		IsActive *bool           `json:"is_active"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON body"})
		return
	}
	if len(body.Graph) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": `"graph" is required`})
		return
	}
	active := true
	if body.IsActive != nil {
		active = *body.IsActive
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := s.data.UpsertWorkflow(ctx, s.unit, model, body.Name, body.Graph, active); err != nil {
		if isValidationErr(err) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		s.dataError(w, "upsert_workflow", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "unit": s.unit, "model": model, "is_active": active})
}

// DELETE /api/workflows/{model} — remove a model's workflow.
func (s *Server) handleDeleteWorkflow(w http.ResponseWriter, r *http.Request) {
	if !s.dataReady(w) {
		return
	}
	model := r.PathValue("model")
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := s.data.DeleteWorkflow(ctx, s.unit, model); err != nil {
		if err.Error() == notFoundMsg {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "no workflow for this model"})
			return
		}
		s.dataError(w, "delete_workflow", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": model})
}

// POST /api/workflows/test — dry-run a graph against a sample payload. Pure
// compute (no database), so the editor can preview results before saving.
func (s *Server) handleTestWorkflow(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Graph   json.RawMessage `json:"graph"`
		Payload map[string]any  `json:"payload"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON body"})
		return
	}
	var g flow.Graph
	if err := json.Unmarshal(body.Graph, &g); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid graph JSON"})
		return
	}
	if err := g.Validate(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	res, err := g.Evaluate(body.Payload)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, res)
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

func (s *Server) unitParam(r *http.Request) string {
	if u := strings.TrimSpace(r.URL.Query().Get("unit")); u != "" {
		return u
	}
	return s.unit
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

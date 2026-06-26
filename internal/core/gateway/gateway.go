// Package gateway is the protocol-agnostic framework core. It runs the device
// TCP accept loop, owns shared dependencies (config, logger, message builder,
// data sinks, device authenticator), and dispatches decoded frames to a
// unit-type plugin that implements Protocol. One gateway process serves exactly
// one Protocol.
package gateway

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"runtime/debug"
	"sync"
	"time"

	"github.com/dfm/device-gateway/internal/core/config"
	"github.com/dfm/device-gateway/internal/core/device"
	"github.com/dfm/device-gateway/internal/core/logging"
	"github.com/dfm/device-gateway/internal/core/mapping"
	"github.com/dfm/device-gateway/internal/core/media"
	"github.com/dfm/device-gateway/internal/core/message"
)

// StreamInfo describes a started live video stream.
type StreamInfo struct {
	SessionID string `json:"session_id"`
	HLSPath   string `json:"hls_path"` // <serial>/<camera>/<profile>/stream.m3u8
}

// ClipRequest asks a device to upload a recorded clip for a camera/time window.
type ClipRequest struct {
	Camera   int   `json:"camera"`
	Profile  int   `json:"profile"`
	StartUTC int64 `json:"start_utc"`
	EndUTC   int64 `json:"end_utc"`
	Audio    bool  `json:"audio"`
}

// ClipInfo identifies a clip download that has been requested (the .mp4 arrives
// asynchronously; poll the clips API for status/progress).
type ClipInfo struct {
	ClipID    int64  `json:"clip_id"`
	SessionID string `json:"session_id"`
	Status    string `json:"status"`
}

// Recording is one file the device reports for a camera/time window (from a file
// query). StartUTC/EndUTC are true UTC epochs; DeviceStart/DeviceEnd are the raw
// local wall-clock strings the device reported (useful for diagnosing timezone).
type Recording struct {
	Camera      int    `json:"camera"`
	Profile     int    `json:"profile"`
	StartUTC    int64  `json:"start_utc"`
	EndUTC      int64  `json:"end_utc"`
	FileName    string `json:"file_name"`
	Size        int64  `json:"size"`
	DeviceStart string `json:"device_start"`
	DeviceEnd   string `json:"device_end"`
}

// VideoController is implemented by a protocol session that can start/stop a live
// video stream, list recorded files, and request recorded clips from its device.
// The HTTP API reaches it through the Hub.
type VideoController interface {
	StartLive(ctx context.Context, camera, profile int) (StreamInfo, error)
	StopLive(ctx context.Context, camera, profile int) error
	RequestClip(ctx context.Context, req ClipRequest) (ClipInfo, error)
	QueryRecordings(ctx context.Context, camera, profile int, startUTC, endUTC int64) ([]Recording, error)
}

// SnapshotFile is one still image the device captured: a camera channel and the
// device-side file path it was written to. Fetching the JPEG bytes needs the
// file-transfer path (0x4090), a later milestone.
type SnapshotFile struct {
	Channel    int    `json:"channel"`
	DevicePath string `json:"device_path"`
}

// SnapshotResult is a device's response to an on-demand snapshot request.
type SnapshotResult struct {
	SessionID string         `json:"session_id"`
	Files     []SnapshotFile `json:"files"`
}

// SnapshotFileInfo is one still image the device has stored on its SD card,
// discovered by a file query. DevicePath feeds the file-transfer download.
type SnapshotFileInfo struct {
	Channel    int    `json:"channel"`
	DevicePath string `json:"device_path"`
	Size       int64  `json:"size"`
	UTC        int64  `json:"utc"`         // capture time, true UTC epoch
	DeviceTime string `json:"device_time"` // raw device-local wall-clock
	Kind       string `json:"kind"`        // "general" | "alarm"
}

// Snapshotter is implemented by a protocol session that can capture and retrieve
// still images (Howen). The HTTP API reaches it through the Hub.
//
//   - RequestSnapshot triggers a capture and returns the device-side file paths.
//   - CaptureImage captures one camera and fetches the JPEG inline.
//   - SearchSnapshots lists stills already stored on the device for a window
//     (kind "general" or "alarm").
//   - FetchSnapshotFile downloads one stored still by its device path.
//
// All but RequestSnapshot need the media port/advertise host enabled.
type Snapshotter interface {
	RequestSnapshot(ctx context.Context, channels []int, resolution int) (SnapshotResult, error)
	CaptureImage(ctx context.Context, camera, resolution int) ([]byte, error)
	SearchSnapshots(ctx context.Context, camera int, startUTC, endUTC int64, kind string) ([]SnapshotFileInfo, error)
	FetchSnapshotFile(ctx context.Context, devicePath string) ([]byte, error)
}

// ConfigController is implemented by a protocol session that can read and write
// its device's parameter configuration (Wi-Fi, mobile, server, …). The `sc` map
// is keyed by segment name; each segment is an object of fields.
type ConfigController interface {
	RequestConfig(ctx context.Context, modules []string) (map[string]any, error)
	UpdateConfig(ctx context.Context, sc map[string]any) error
}

// Capabilities declares what a unit type supports. GPS-only trackers leave the
// optional capabilities false so no video/command code is wired in. These are the
// unit's *declared* features; the running gateway's *effective* capabilities
// (declared AND enabled by config) are reported separately — see
// EffectiveCapabilities.
type Capabilities struct {
	HasVideo    bool
	HasCommands bool
	HasConfig   bool // session implements ConfigController
	HasStatus   bool // session implements StatusReporter
}

// EffectiveCapabilities is what the running gateway actually offers right now: the
// unit's declared capabilities gated by runtime configuration (e.g. video needs a
// media advertise host; clips need a database). Surfaced to the admin panel via
// GET /api/gateway/info so it can hide UI for features this build/config lacks.
type EffectiveCapabilities struct {
	HasVideo    bool `json:"has_video"`    // unit HasVideo AND video enabled (MEDIA_ADVERTISE_HOST set)
	HasCommands bool `json:"has_commands"` // unit HasCommands
	HasConfig   bool `json:"has_config"`   // unit HasConfig
	HasStatus   bool `json:"has_status"`   // unit HasStatus
	HasClips    bool `json:"has_clips"`    // video enabled AND database present
	HasMappings bool `json:"has_mappings"` // unit implements MappingProvider
}

// MappingProvider is implemented by a Protocol whose event output is driven by an
// editable code→event mapping table. The app runner uses it to seed the built-in
// defaults and apply the DB-loaded set WITHOUT importing the unit package. A unit
// with no editable mappings (e.g. a plain GPS tracker) does not implement it, and
// the runner skips all mapping wiring.
type MappingProvider interface {
	DefaultMappingEntries() []mapping.Entry
	ApplyMappings(mapping.ByModel)
}

// MappingPruner is an optional MappingProvider extension. PrunableMappings lists
// editable rows the unit handles with built-in logic and never reads from the
// DB; the runner deletes any that older builds seeded so the admin only shows
// rows that actually take effect. A provider that implements it is detected via
// type assertion; one that doesn't keeps every seeded row.
type MappingPruner interface {
	PrunableMappings() []mapping.Prune
}

// MediaListener is a device-side media accept loop (a separate TCP port from the
// control server) that a video-capable unit runs to receive video frames.
type MediaListener interface {
	ListenAndServe(ctx context.Context) error
}

// MediaServerProvider is implemented by a Protocol that runs its own device-side
// media listener. The app runner starts it only when video is enabled (a media
// manager is present). The returned listener is fully configured to bind addr and
// write into mgr/clips.
type MediaServerProvider interface {
	NewMediaServer(addr string, mgr *media.Manager, clips *media.ClipRegistry, snaps *media.SnapshotFetch, log *logging.Logger) MediaListener
}

// IdleTimeoutProvider lets a unit override the framework's default per-connection
// idle timeout (the read deadline). A unit whose devices speak infrequently — e.g.
// a tracker that only heartbeats every few minutes — implements it so the read
// loop is not closed between messages. Detected by the app runner via type
// assertion; units that don't implement it keep the default.
type IdleTimeoutProvider interface {
	IdleTimeout() time.Duration
}

// DefaultPort lets a unit declare the device TCP port it binds when no per-unit
// port override is set. The app runner resolves a unit's port as <UNIT>_PORT env →
// DefaultDevicePort() → the generic LISTEN_PORT. Detected via type assertion; a
// unit that doesn't implement it falls back to LISTEN_PORT. This is what lets one
// process host several units, each on its own port.
type DefaultPort interface {
	DefaultDevicePort() int
}

// DefaultMediaPort lets a video-capable unit declare its own media (device-side
// video) port default, so several video units in one process don't collide on the
// shared MEDIA_PORT. Detected via type assertion; a unit that doesn't implement it
// uses the global MEDIA_PORT. The per-unit stored override still wins over this.
type DefaultMediaPort interface {
	DefaultMediaPort() int
}

// SettingField describes one editable gateway-side, unit-type-level setting (e.g.
// a GPS tracker's timezone offset) — distinct from per-device parameter config.
// A unit declares its fields via ConfigurableUnit; the admin renders them as that
// unit's settings screen; values are stored per unit, hot-reloaded, and read by
// the running session through Deps.UnitSettings.
type SettingField struct {
	Key     string   `json:"key"`
	Label   string   `json:"label"`
	Type    string   `json:"type"` // "string" | "number" | "bool" | "select"
	Default string   `json:"default"`
	Options []string `json:"options,omitempty"` // for "select"
	Help    string   `json:"help,omitempty"`
	Group   string   `json:"group,omitempty"`
}

// ConfigurableUnit is implemented by a unit that exposes editable gateway-side
// settings. Detected by the app runner via type assertion; a unit that doesn't
// implement it has no settings screen.
type ConfigurableUnit interface {
	SettingsSchema() []SettingField
}

// Frame is one decoded protocol message.
type Frame struct {
	Type    int
	Payload []byte
}

// Protocol is the contract a unit-type plugin implements. To add a new server,
// implement this interface and point a cmd/<unit>/main.go at it.
type Protocol interface {
	// Name is the unit type, e.g. "howen". Surfaced in logs and webhook fields.
	Name() string
	// Capabilities declares optional features.
	Capabilities() Capabilities
	// ReadFrame decodes exactly one frame from the buffered connection stream.
	// It must return io.EOF when the peer closes cleanly.
	ReadFrame(r *bufio.Reader) (Frame, error)
	// NewSession creates per-connection protocol state.
	NewSession(c *Conn) Session
}

// Session handles the frames of a single connection.
type Session interface {
	// OnFrame processes one frame. Returning an error closes the connection.
	OnFrame(ctx context.Context, f Frame) error
	// OnClose runs once when the connection ends.
	OnClose(ctx context.Context)
}

// Sink consumes a built universal message (e.g. PostgreSQL, webhook). A message
// is built once and handed to every configured sink, so the per-device seq_no is
// incremented exactly once regardless of how many sinks are active.
type Sink interface {
	Consume(ctx context.Context, in message.Inbound, msg message.Universal) error
}

// DeviceErrorRecorder persists an error a device reported over its connection
// (e.g. a media/clip/event upload failure). The signature is primitives only so
// the storage layer satisfies it structurally, without importing this package.
// Implemented by *postgres.Store.
type DeviceErrorRecorder interface {
	RecordDeviceError(ctx context.Context, serial, category, message, remoteAddr string, remotePort int, raw []byte) error
}

// Deps bundles the shared dependencies handed to each connection.
type Deps struct {
	Config  config.Config
	Log     *logging.Logger
	Builder *message.Builder
	Sinks   []Sink
	Auth    device.Authenticator
	// Hub tracks connected devices for the HTTP control API. May be nil when the
	// HTTP API is disabled; sessions must nil-check before registering.
	Hub *Hub
	// DeviceErrors persists device-reported errors. May be nil (no database);
	// Conn.EmitDeviceError nil-checks before using it.
	DeviceErrors DeviceErrorRecorder

	// Media runs live HLS streams; nil when video is disabled. A protocol
	// session that supports video reads it (and MediaAdvertiseHost) to start
	// streams; it must nil-check.
	Media *media.Manager
	// Clips tracks recorded-clip downloads (.mp4 to the server bucket); nil when
	// video/clips are disabled. A protocol session must nil-check.
	Clips *media.ClipRegistry
	// Snapshots tracks in-flight in-memory file fetches (Howen 0x4090
	// file-transfer, e.g. snapshot JPEGs). Nil when video is disabled; a session
	// must nil-check.
	Snapshots *media.SnapshotFetch
	// MediaAdvertiseHost is the host:port the device dials back for media frames
	// (embedded in the start-stream/playback command). Empty when video is disabled.
	MediaAdvertiseHost string
	// DeviceTZOffsetHours localizes clip playback windows to the device's local
	// clock (Howen indexes recordings by local wall-clock). 0 = UTC.
	DeviceTZOffsetHours float64

	// UnitSettings holds this unit's editable gateway-side settings (see
	// ConfigurableUnit). Per-unit; nil when the unit declares no settings. A
	// session must nil-check before reading. Hot-swapped on admin edits.
	UnitSettings *UnitSettings
}

// Conn wraps a device socket with framework dependencies.
type Conn struct {
	net.Conn
	Deps   Deps
	writeM sync.Mutex
}

// WriteFrame writes raw bytes to the device, serialized against concurrent
// writers (command paths may write outside the read goroutine).
func (c *Conn) WriteFrame(b []byte) error {
	c.writeM.Lock()
	defer c.writeM.Unlock()
	_, err := c.Conn.Write(b)
	return err
}

// RemoteIP returns the peer IP without the port.
func (c *Conn) RemoteIP() string {
	host, _, err := net.SplitHostPort(c.RemoteAddr().String())
	if err != nil {
		return c.RemoteAddr().String()
	}
	return host
}

// RemotePort returns the peer TCP port (0 if unknown).
func (c *Conn) RemotePort() int {
	if tcp, ok := c.RemoteAddr().(*net.TCPAddr); ok {
		return tcp.Port
	}
	return 0
}

// LocalIP returns the local IP the device connected to.
func (c *Conn) LocalIP() string {
	host, _, err := net.SplitHostPort(c.LocalAddr().String())
	if err != nil {
		return c.LocalAddr().String()
	}
	return host
}

// Emit builds the universal message from a normalized payload once and delivers
// it to every configured sink asynchronously (fire-and-forget) so the read loop
// is never blocked. Every protocol plugin uses this single path — there is no
// per-unit storage code.
//
// msgType is "gps" or "event"; payload is the normalized field map the universal
// builder understands (latitude, longitude, speed, event, etc.).
func (c *Conn) Emit(serial, make, model, msgType string, payload map[string]any) {
	in := message.Inbound{
		Serial:     serial,
		Make:       make,
		Model:      model,
		Type:       msgType,
		Port:       c.Deps.Config.ListenPort,
		Network:    message.Network{RemoteAddress: c.RemoteIP(), RemotePort: c.RemotePort()},
		Payload:    payload,
		ReceivedAt: time.Now().UTC().Format("2006-01-02T15:04:05.000Z07:00"),
	}
	msg := c.Deps.Builder.Build(in)
	log := c.Deps.Log
	for _, sink := range c.Deps.Sinks {
		sink := sink
		go func() {
			defer recoverGo(log, "emit_sink")
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			if err := sink.Consume(ctx, in, msg); err != nil {
				log.Debug(map[string]any{"event": "sink_error", "type": msgType, "error": err.Error()})
			}
		}()
	}
}

// recoverGo contains a panic in a fire-and-forget goroutine spawned off the read
// loop (a sink Consume, a device-error write). Without it such a panic crashes
// the whole process and every co-hosted unit with it. The Error log is persisted
// to gateway_errors. Mirrors the per-connection recover in handle().
func recoverGo(log *logging.Logger, where string) {
	if r := recover(); r != nil {
		log.Error(map[string]any{
			"event": "panic_recovered",
			"where": where,
			"panic": fmt.Sprint(r),
			"stack": string(debug.Stack()),
		})
	}
}

// EmitDeviceError persists an error a device reported over this connection,
// fire-and-forget so the read loop is never blocked. Remote address/port are
// taken from the connection. No-op when no device-error recorder is configured
// (e.g. no database). category may be empty; raw is the original payload (stored
// as JSONB when it is valid JSON) and may be nil.
func (c *Conn) EmitDeviceError(serial, category, message string, raw []byte) {
	rec := c.Deps.DeviceErrors
	if rec == nil {
		return
	}
	remoteAddr, remotePort := c.RemoteIP(), c.RemotePort()
	log := c.Deps.Log
	go func() {
		defer recoverGo(log, "device_error")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := rec.RecordDeviceError(ctx, serial, category, message, remoteAddr, remotePort, raw); err != nil {
			log.Debug(map[string]any{"event": "device_error_persist_failed", "serial": serial, "error": err.Error()})
		}
	}()
}

// Server binds a Protocol to a listening socket.
type Server struct {
	proto       Protocol
	deps        Deps
	idleTimeout time.Duration
}

// New constructs a Server for the given protocol and dependencies.
func New(proto Protocol, deps Deps) *Server {
	return &Server{proto: proto, deps: deps, idleTimeout: 3 * time.Minute}
}

// SetIdleTimeout overrides the per-connection idle timeout.
func (s *Server) SetIdleTimeout(d time.Duration) { s.idleTimeout = d }

// ListenAndServe binds the configured host:port and serves until ctx is done.
func (s *Server) ListenAndServe(ctx context.Context) error {
	addr := net.JoinHostPort(s.deps.Config.ListenHost, itoa(s.deps.Config.ListenPort))
	lc := net.ListenConfig{}
	ln, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	s.deps.Log.Info(map[string]any{
		"event": "listening", "host": s.deps.Config.ListenHost,
		"port": s.deps.Config.ListenPort, "unit": s.proto.Name(),
		"capabilities": s.proto.Capabilities(),
	})
	return s.Serve(ctx, ln)
}

// Serve runs the accept loop on a caller-provided listener until ctx is done.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	var wg sync.WaitGroup
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				wg.Wait()
				return nil
			}
			s.deps.Log.Error(map[string]any{"event": "accept_error", "error": err.Error()})
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.handle(ctx, conn)
		}()
	}
}

func (s *Server) handle(ctx context.Context, raw net.Conn) {
	// Contain a panic to this one connection. ReadFrame/OnFrame run on
	// device-supplied bytes, and one process now hosts several unit types — an
	// unhandled panic here would otherwise crash the process and take down every
	// other unit's listener too. Registered first so it runs last, after the
	// OnClose/raw.Close defers below have unwound. The Error log is also persisted
	// to gateway_errors for visibility.
	defer func() {
		if r := recover(); r != nil {
			s.deps.Log.Error(map[string]any{
				"event":  "panic_recovered",
				"unit":   s.proto.Name(),
				"remote": raw.RemoteAddr().String(),
				"panic":  fmt.Sprint(r),
				"stack":  string(debug.Stack()),
			})
		}
	}()

	if tcp, ok := raw.(*net.TCPConn); ok {
		_ = tcp.SetKeepAlive(true)
		_ = tcp.SetKeepAlivePeriod(30 * time.Second)
	}
	conn := &Conn{Conn: raw, Deps: s.deps}
	log := s.deps.Log
	log.Debug(map[string]any{"event": "connection", "remote": conn.RemoteAddr().String()})

	sess := s.proto.NewSession(conn)
	defer sess.OnClose(ctx)
	defer raw.Close()

	// Promptly unblock the read loop on shutdown. An idle connection otherwise
	// blocks in ReadFrame until its read deadline (up to idleTimeout — minutes),
	// stalling graceful shutdown's wg.Wait(). The watcher, tied to a child of ctx,
	// expires the read deadline the instant ctx is cancelled; on a normal handle()
	// return cancelWatch fires first, so it does nothing and never leaks.
	watchCtx, cancelWatch := context.WithCancel(ctx)
	defer cancelWatch()
	go func() {
		<-watchCtx.Done()
		if ctx.Err() != nil { // real shutdown, not a normal return
			_ = raw.SetReadDeadline(time.Now())
		}
	}()

	r := bufio.NewReader(raw)
	for {
		if ctx.Err() != nil { // shutdown: don't block on another read
			return
		}
		if s.idleTimeout > 0 {
			_ = raw.SetReadDeadline(time.Now().Add(s.idleTimeout))
		}
		frame, err := s.proto.ReadFrame(r)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				log.Debug(map[string]any{"event": "peer_closed"})
			} else if ne, ok := err.(net.Error); ok && ne.Timeout() {
				if ctx.Err() != nil {
					log.Debug(map[string]any{"event": "shutdown"})
				} else {
					log.Debug(map[string]any{"event": "idle_timeout"})
				}
			} else {
				log.Debug(map[string]any{"event": "read_error", "error": err.Error()})
			}
			return
		}
		if err := sess.OnFrame(ctx, frame); err != nil {
			log.Debug(map[string]any{"event": "frame_error", "error": err.Error()})
			return
		}
	}
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

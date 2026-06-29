package gateway

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"
)

// Command-dispatch errors. The HTTP API maps these to status codes.
var (
	ErrNotConnected       = errors.New("unit not connected")
	ErrUnsupportedCommand = errors.New("unsupported command")
	ErrInvalidCommand     = errors.New("invalid command")
	ErrCommandTimeout     = errors.New("device response timeout")
	// ErrDeviceSleeping means the device is connected but in standby and won't
	// service video/playback until woken.
	ErrDeviceSleeping = errors.New("device is in standby")
)

// DeviceInfo is public metadata about a connected device. State is the live
// connection state: "online" (awake) or "sleep" (standby) — a connected device
// in standby won't service video/playback until woken.
type DeviceInfo struct {
	Serial      string    `json:"serial"`
	Protocol    string    `json:"protocol"`
	Model       string    `json:"model,omitempty"`
	RemoteAddr  string    `json:"remote_addr"`
	ConnectedAt time.Time `json:"connected_at"`
	State       string    `json:"state"`
	Commands    []string  `json:"commands"`
}

// Command is a control request to send to a device.
type Command struct {
	Type    string         `json:"type"`
	Payload map[string]any `json:"payload,omitempty"`
}

// CommandResult is a device's response to a command.
type CommandResult struct {
	Data       map[string]any `json:"data"`
	ReceivedAt time.Time      `json:"received_at"`
}

// Commander is implemented by a protocol session that can talk to its device.
type Commander interface {
	SendCommand(ctx context.Context, cmd Command) (CommandResult, error)
	SupportedCommands() []string
}

// StatusReporter is implemented by a session that can report its device's latest
// status snapshot (network/4G, modules, storage, IO, …) for the detail view.
type StatusReporter interface {
	Status() (map[string]any, bool)
}

// Hub is the registry of currently-connected devices. It is protocol-agnostic:
// any unit-type session that supports commands registers itself, and the HTTP
// API queries/commands through here. Safe for concurrent use.
type Hub struct {
	mu      sync.RWMutex
	entries map[string]*hubEntry
}

type hubEntry struct {
	info      DeviceInfo
	commander Commander
}

// NewHub creates an empty hub.
func NewHub() *Hub {
	return &Hub{entries: map[string]*hubEntry{}}
}

// Register adds (or replaces) a connected device. The supported command list is
// taken from the commander.
func (h *Hub) Register(info DeviceInfo, c Commander) {
	info.Commands = c.SupportedCommands()
	if info.State == "" {
		info.State = "online"
	}
	h.mu.Lock()
	h.entries[info.Serial] = &hubEntry{info: info, commander: c}
	h.mu.Unlock()
}

// Unregister removes a device, but only if the registered commander is still the
// given one (so a reconnect that replaced the entry is not clobbered by the old
// connection's cleanup). Returns true if it actually removed this session — the
// caller uses that to avoid marking a reconnected device offline.
func (h *Hub) Unregister(serial string, c Commander) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if e, ok := h.entries[serial]; ok && e.commander == c {
		delete(h.entries, serial)
		return true
	}
	return false
}

// SetState updates a connected device's live state (online/sleep), but only if
// the given commander is still the registered one (ignores stale sessions).
func (h *Hub) SetState(serial string, c Commander, state string) {
	h.mu.Lock()
	if e, ok := h.entries[serial]; ok && e.commander == c {
		e.info.State = state
	}
	h.mu.Unlock()
}

// List returns all connected devices, sorted by serial.
func (h *Hub) List() []DeviceInfo {
	h.mu.RLock()
	out := make([]DeviceInfo, 0, len(h.entries))
	for _, e := range h.entries {
		out = append(out, e.info)
	}
	h.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Serial < out[j].Serial })
	return out
}

// Get returns one device's info.
func (h *Hub) Get(serial string) (DeviceInfo, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if e, ok := h.entries[serial]; ok {
		return e.info, true
	}
	return DeviceInfo{}, false
}

// Video returns the device's video controller, if its session supports video.
func (h *Hub) Video(serial string) (VideoController, bool) {
	h.mu.RLock()
	e, ok := h.entries[serial]
	h.mu.RUnlock()
	if !ok {
		return nil, false
	}
	vc, ok := e.commander.(VideoController)
	return vc, ok
}

// Review returns a connected device's recorded-playback controller, if its session
// implements ReviewController.
func (h *Hub) Review(serial string) (ReviewController, bool) {
	h.mu.RLock()
	e, ok := h.entries[serial]
	h.mu.RUnlock()
	if !ok {
		return nil, false
	}
	rc, ok := e.commander.(ReviewController)
	return rc, ok
}

// Snapshotter returns a connected device's snapshot capability, if its session
// supports on-demand still-image capture.
func (h *Hub) Snapshotter(serial string) (Snapshotter, bool) {
	h.mu.RLock()
	e, ok := h.entries[serial]
	h.mu.RUnlock()
	if !ok {
		return nil, false
	}
	s, ok := e.commander.(Snapshotter)
	return s, ok
}

// Status returns a connected device's latest status snapshot, if its session
// reports one.
func (h *Hub) Status(serial string) (map[string]any, bool) {
	h.mu.RLock()
	e, ok := h.entries[serial]
	h.mu.RUnlock()
	if !ok {
		return nil, false
	}
	sr, ok := e.commander.(StatusReporter)
	if !ok {
		return nil, false
	}
	return sr.Status()
}

// Config returns the device's config controller, if its session supports it.
func (h *Hub) Config(serial string) (ConfigController, bool) {
	h.mu.RLock()
	e, ok := h.entries[serial]
	h.mu.RUnlock()
	if !ok {
		return nil, false
	}
	cc, ok := e.commander.(ConfigController)
	return cc, ok
}

// Send dispatches a command to a connected device and returns its response.
// Returns ErrNotConnected if the device is not currently connected.
func (h *Hub) Send(ctx context.Context, serial string, cmd Command) (CommandResult, error) {
	h.mu.RLock()
	e, ok := h.entries[serial]
	h.mu.RUnlock()
	if !ok {
		return CommandResult{}, ErrNotConnected
	}
	return e.commander.SendCommand(ctx, cmd)
}

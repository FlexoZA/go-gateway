// Package howen implements the Howen H-Protocol unit type as a gateway plugin.
//
// Milestone 1 scope: device registration/approval, GPS status, and alarm/event
// telemetry forwarded to the universal webhook. Video/media and device control
// are declared as capabilities for later milestones but not wired here.
package howen

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dfm/device-gateway/internal/core/device"
	"github.com/dfm/device-gateway/internal/core/gateway"
)

const (
	maxPayloadBytes = 1024 * 1024 // 1 MiB, matches HOWEN_MAX_PAYLOAD_BYTES
	// Howen `ct` bitmask 173 = bits 0,2,3,5,7 (location, basic, module-working,
	// mobile-network, hard-disk). Same for GPS and alarm subscriptions.
	gpsSubscriptionContent   = "173"
	alarmSubscriptionContent = "173"
	defaultModel             = "Hero-MC30-02"
)

// Protocol is the Howen unit-type plugin.
type Protocol struct{}

// New returns a Howen protocol plugin.
func New() *Protocol { return &Protocol{} }

func (*Protocol) Name() string { return "howen" }

func (*Protocol) Capabilities() gateway.Capabilities {
	// Control commands and live video are implemented (video is active only when
	// the gateway is configured with a media advertise host).
	return gateway.Capabilities{HasVideo: true, HasCommands: true}
}

// ReadFrame decodes one H-Protocol frame from the stream.
func (*Protocol) ReadFrame(r *bufio.Reader) (gateway.Frame, error) {
	var header [howenHeaderSize]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return gateway.Frame{}, err
	}
	h, err := readHowenFrameHeader(header[:])
	if err != nil {
		return gateway.Frame{}, err
	}
	if h.PayloadLength > maxPayloadBytes {
		return gateway.Frame{}, fmt.Errorf("payload too large: %d", h.PayloadLength)
	}
	payload := make([]byte, h.PayloadLength)
	if _, err := io.ReadFull(r, payload); err != nil {
		return gateway.Frame{}, err
	}
	return gateway.Frame{Type: h.Type, Payload: payload}, nil
}

func (*Protocol) NewSession(c *gateway.Conn) gateway.Session {
	return &session{conn: c, pending: map[string]chan map[string]any{}}
}

type gateStatus int

const (
	gateNew gateStatus = iota
	gateApproved
	gateQuarantined
)

type session struct {
	conn         *gateway.Conn
	serial       string
	imei         string
	model        string
	howenSession string
	gate         gateStatus
	lifecycle    string // "online" | "sleep" | "offline"

	// pending correlates command sessions (ss) to a waiting SendCommand. Guarded
	// by pendingMu because SendCommand runs on the HTTP goroutine while the read
	// loop delivers responses.
	pendingMu sync.Mutex
	pending   map[string]chan map[string]any
}

// OnFrame dispatches a decoded frame.
func (s *session) OnFrame(ctx context.Context, f gateway.Frame) error {
	log := s.conn.Deps.Log.With("tcp/howen")
	switch f.Type {
	case msgHeartbeat:
		return s.conn.WriteFrame(buildHowenEmptyFrame(msgHeartbeat))

	case msgSignalRegister:
		return s.handleRegistration(ctx, f.Payload)

	case msgGpsSubscribeResponse, msgAlarmSubscribeResponse:
		if obj, err := parseHowenJSONObject(f.Payload); err == nil {
			log.Debug(map[string]any{"event": "subscription_response", "type": f.Type, "err": obj["err"], "ss": obj["ss"]})
		}
		return nil

	case msgGpsStatus:
		return s.handleGpsStatus(ctx, f.Payload)

	case msgAlarmData:
		return s.handleAlarmData(ctx, f.Payload)

	case msgDeviceAnswer:
		// Control-command acknowledgement — route to the waiting SendCommand.
		if obj, err := parseHowenJSONObject(f.Payload); err == nil {
			s.resolveDeviceAnswer(obj)
		}
		return nil

	case msgLivePreviewResponse:
		// Live-preview ack (0x1010) — route to the waiting StartLive/StopLive by ss.
		if obj, err := parseHowenJSONObject(f.Payload); err == nil {
			s.resolveDeviceAnswer(obj)
		}
		return nil

	case msgPlaybackResponse:
		// Playback (clip) request ack (0x1070) — route to the waiting RequestClip
		// by ss, same as the live-preview ack.
		if obj, err := parseHowenJSONObject(f.Payload); err == nil {
			s.resolveDeviceAnswer(obj)
		}
		return nil

	case msgPlaybackEnd:
		// The device finished uploading a clip (0x1071) — finalize the .mp4.
		if obj, err := parseHowenJSONObject(f.Payload); err == nil {
			if ss := toString(obj["ss"]); ss != "" && s.conn.Deps.Clips != nil {
				s.conn.Deps.Clips.Finish(ss)
			}
		}
		return nil

	case msgFileQueryResponse, msgParamConfigResponse, msgSnapshotResponse:
		// Other video/config responses: handled in later milestones.
		log.Debug(map[string]any{"event": "command_response_ignored", "type": f.Type})
		return nil

	default:
		log.Debug(map[string]any{"event": "unsupported_message", "type": f.Type, "serial": s.serialOrUnknown()})
		return nil
	}
}

func (s *session) OnClose(ctx context.Context) {
	if s.serial == "" || s.gate != gateApproved {
		return
	}
	if s.conn.Deps.Hub != nil {
		s.conn.Deps.Hub.Unregister(s.serial, s)
	}
	if err := s.conn.Deps.Auth.UpdateStatus(ctx, s.serial, "offline"); err != nil {
		s.conn.Deps.Log.With("tcp/howen").Debug(map[string]any{
			"event": "device_status_update_failed", "serial": s.serial, "status": "offline", "error": err.Error(),
		})
	}
	s.conn.Deps.Log.With("tcp/howen").Debug(map[string]any{"event": "close", "serial": s.serial})
}

func (s *session) handleRegistration(ctx context.Context, payload []byte) error {
	log := s.conn.Deps.Log.With("tcp/howen")
	reg, err := parseHowenJSONObject(payload)
	if err != nil {
		log.Debug(map[string]any{"event": "registration_parse_error", "remote": s.conn.RemoteAddr().String(), "error": err.Error()})
		return fmt.Errorf("registration parse error: %w", err)
	}
	deviceNumber := strings.TrimSpace(toString(reg["dn"]))
	if deviceNumber == "" {
		log.Debug(map[string]any{"event": "registration_missing_dn", "remote": s.conn.RemoteAddr().String()})
		return fmt.Errorf("registration missing dn")
	}

	s.serial = device.NormalizeSerial(deviceNumber)
	s.imei = strings.TrimSpace(toString(reg["imei"]))
	s.model = defaultModel
	if fw := strings.TrimSpace(toString(reg["fw"])); fw != "" {
		s.model = fw
	}
	s.howenSession = toString(reg["ss"])

	log.Debug(map[string]any{"event": "signal_register", "serial": s.serial, "model": s.model, "remote": s.conn.RemoteAddr().String()})

	info := device.RegisterInfo{
		Serial:   s.serial,
		Protocol: "howen",
		RemoteIP: s.conn.RemoteIP(),
		Meta: map[string]any{
			"message_type": "signal_register",
			"dn":           reg["dn"],
			"imei":         reg["imei"],
			"fw":           reg["fw"],
			"ss":           reg["ss"],
		},
	}
	result, err := s.conn.Deps.Auth.Authorize(ctx, info)
	if err != nil {
		log.Error(map[string]any{"event": "device_gate_error", "serial": s.serial, "error": err.Error()})
		return fmt.Errorf("device authorize failed: %w", err)
	}
	if !result.Known {
		s.gate = gateQuarantined
		log.Info(map[string]any{"event": "unknown_device_quarantined", "serial": s.serial})
		_ = s.conn.WriteFrame(buildHowenJSONFrame(msgSignalRegisterResponse, map[string]any{"ss": s.howenSession, "err": "1"}))
		return fmt.Errorf("unknown device rejected")
	}

	s.gate = gateApproved
	s.lifecycle = "online"
	if err := s.conn.WriteFrame(buildHowenJSONFrame(msgSignalRegisterResponse, map[string]any{"ss": s.howenSession, "err": "0"})); err != nil {
		return err
	}
	if err := s.conn.WriteFrame(s.buildGpsSubscriptionFrame()); err != nil {
		return err
	}
	if err := s.conn.WriteFrame(s.buildAlarmSubscriptionFrame()); err != nil {
		return err
	}
	if err := s.conn.Deps.Auth.UpdateStatus(ctx, s.serial, "online"); err != nil {
		log.Debug(map[string]any{"event": "device_status_update_failed", "serial": s.serial, "status": "online", "error": err.Error()})
	}
	// Make the device reachable by the HTTP control API.
	if s.conn.Deps.Hub != nil {
		s.conn.Deps.Hub.Register(gateway.DeviceInfo{
			Serial:      s.serial,
			Protocol:    "howen",
			Model:       s.model,
			RemoteAddr:  s.conn.RemoteAddr().String(),
			ConnectedAt: time.Now().UTC(),
		}, s)
	}
	log.Info(map[string]any{"event": "device_approved", "serial": s.serial, "protocol": result.Protocol})
	return nil
}

func (s *session) buildGpsSubscriptionFrame() []byte {
	sess := fmt.Sprintf("status-%s-%s", s.serial, hexNow())
	return buildHowenJSONFrame(msgGpsSubscribe, map[string]any{"ss": sess, "ct": gpsSubscriptionContent, "rt": "0"})
}

func (s *session) buildAlarmSubscriptionFrame() []byte {
	sess := fmt.Sprintf("alarm-%s-%s", s.serial, hexNow())
	return buildHowenJSONFrame(msgAlarmSubscribe, map[string]any{"ss": sess, "ct": alarmSubscriptionContent, "rt": "0", "ack": "0"})
}

func (s *session) handleGpsStatus(ctx context.Context, payload []byte) error {
	// Always ACK, even before approval, to keep the device's subscription alive.
	if err := s.conn.WriteFrame(buildHowenEmptyFrame(msgGpsStatusAck)); err != nil {
		return err
	}
	if s.gate != gateApproved {
		return nil
	}
	parsed := parseHowenStatusPayload(payload)
	if parsed != nil && parsed.Status != nil {
		s.reconcileLifecycle(ctx, parsed.Status)
	}
	if parsed == nil || parsed.Status == nil || parsed.Status.Location == nil {
		s.conn.Deps.Log.With("tcp/howen").Debug(map[string]any{"event": "gps_status_without_location", "serial": s.serial})
		return nil
	}
	p := buildGpsPayload(parsed.Status, s.imei)
	s.conn.Emit(s.serial, "howen", s.model, "gps", p)
	return nil
}

func (s *session) handleAlarmData(ctx context.Context, payload []byte) error {
	if s.gate != gateApproved {
		return nil
	}
	parsed := parseHowenAlarmPayload(payload)
	if parsed == nil || parsed.Alarm == nil {
		s.conn.Deps.Log.With("tcp/howen").Debug(map[string]any{"event": "alarm_parse_failed", "serial": s.serialOrUnknown()})
		return nil
	}
	if parsed.Status != nil {
		s.reconcileLifecycle(ctx, parsed.Status)
	}
	p := buildEventPayload(parsed, s.imei)
	// A model-specific visual workflow, if defined, overrides the built-in
	// table mapping (strictly per model — see internal/howen/workflows.go).
	usedWorkflow := applyModelWorkflow(s.model, parsed, p)
	s.conn.Deps.Log.With("tcp/howen").Debug(map[string]any{
		"event": "alarm_forward", "serial": s.serial, "ec": parsed.EC,
		"mapped_events": p["event"], "workflow": usedWorkflow, "model": s.model,
	})
	s.conn.Emit(s.serial, "howen", s.model, "event", p)
	return nil
}

// reconcileLifecycle flips device status between online/sleep based on the
// reported sleep_mode flag, deduping on the last-known value.
func (s *session) reconcileLifecycle(ctx context.Context, status *howenStatusData) {
	sleeping := status.BasicStatus != nil && status.BasicStatus.SleepMode != 0
	desired := "online"
	if sleeping {
		desired = "sleep"
	}
	if s.lifecycle == desired {
		return
	}
	s.lifecycle = desired
	if err := s.conn.Deps.Auth.UpdateStatus(ctx, s.serial, desired); err != nil {
		s.conn.Deps.Log.With("tcp/howen").Debug(map[string]any{
			"event": "device_status_update_failed", "serial": s.serial, "status": desired, "error": err.Error(),
		})
	}
}

func (s *session) serialOrUnknown() string {
	if s.serial == "" {
		return "unknown"
	}
	return s.serial
}

func hexNow() string {
	return strings.ToUpper(strconv.FormatInt(time.Now().UnixMilli(), 16))
}

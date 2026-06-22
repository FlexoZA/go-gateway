package howen

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/dfm/device-gateway/internal/core/gateway"
)

// howenControlDef defines a device-control command. Each builds the JSON body
// (the session id is added by the dispatcher) and is acknowledged by
// DEVICE_ANSWER (0x1100), correlated by session id.
type howenControlDef struct {
	msgType int
	danger  bool // destructive; requires payload.confirm = true
	build   func(p map[string]any) (map[string]any, error)
}

func emptyBody(map[string]any) (map[string]any, error) { return map[string]any{}, nil }

// howenControlCommands is the supported control-command catalog (§2.14). Video
// (stream/clip/query) and parameter-config commands are intentionally excluded
// for now — they arrive with the media milestone.
var howenControlCommands = map[string]howenControlDef{
	"reboot_unit":       {msgType: msgRestart, build: emptyBody},
	"clear_alarm":       {msgType: msgClearAlarm, build: emptyBody},
	"wake_device":       {msgType: msgWakeDevice, build: emptyBody},
	"gsensor_calibrate": {msgType: msgGsensorCalibrate, build: emptyBody},
	"sync_time": {msgType: msgSyncTime, build: func(p map[string]any) (map[string]any, error) {
		body := map[string]any{}
		if tm, ok := optStr(p, "tm"); ok {
			body["tm"] = tm
		}
		return body, nil
	}},
	"osd_speed": {msgType: msgOsdSpeed, build: func(p map[string]any) (map[string]any, error) {
		v, err := reqStr(p, "ods")
		if err != nil {
			return nil, err
		}
		return map[string]any{"ods": v}, nil
	}},
	"send_message": {msgType: msgSendMessage, build: func(p map[string]any) (map[string]any, error) {
		text, err := reqStr(p, "text")
		if err != nil {
			return nil, err
		}
		tp, ok := optStr(p, "tp")
		if !ok {
			tp = "1"
		}
		return map[string]any{"tp": tp, "text": text}, nil
	}},
	"reset_mileage": {msgType: msgResetMileage, build: func(p map[string]any) (map[string]any, error) {
		raw, ok := p["mile"]
		if !ok {
			return nil, errors.New(`missing field "mile"`)
		}
		n, ok := numberOrNullInt(raw)
		if !ok {
			return nil, errors.New(`"mile" must be a number`)
		}
		return map[string]any{"mile": n}, nil
	}},
	"recording_control": {msgType: msgRecordControl, build: func(p map[string]any) (map[string]any, error) {
		body := map[string]any{}
		if ol, ok := optStr(p, "open"); ok {
			body["ol"] = ol
		}
		if cl, ok := optStr(p, "close"); ok {
			body["cl"] = cl
		}
		if len(body) == 0 {
			return nil, errors.New(`provide "open" or "close" channel list`)
		}
		return body, nil
	}},
	"factory_reset": {msgType: msgFactoryReset, danger: true, build: emptyBody},
	"format_disk": {msgType: msgFormatDisk, danger: true, build: func(p map[string]any) (map[string]any, error) {
		v, err := reqStr(p, "disk")
		if err != nil {
			return nil, err
		}
		return map[string]any{"num": v}, nil
	}},
	"vehicle_control": {msgType: msgVehicleControl, danger: true, build: func(p map[string]any) (map[string]any, error) {
		act, err := reqStr(p, "act")
		if err != nil {
			return nil, err
		}
		body := map[string]any{"act": act}
		if door, ok := optStr(p, "door"); ok {
			body["do"] = door
		}
		return body, nil
	}},
}

// SupportedCommands lists the command types this session accepts (sorted).
func (s *session) SupportedCommands() []string {
	out := make([]string, 0, len(howenControlCommands))
	for k := range howenControlCommands {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// SendCommand builds the command frame, sends it, and waits for the device's
// DEVICE_ANSWER (correlated by session id) or the context deadline.
func (s *session) SendCommand(ctx context.Context, cmd gateway.Command) (gateway.CommandResult, error) {
	def, ok := howenControlCommands[cmd.Type]
	if !ok {
		return gateway.CommandResult{}, fmt.Errorf("%w: %s", gateway.ErrUnsupportedCommand, cmd.Type)
	}
	if def.danger && !isConfirmed(cmd.Payload) {
		return gateway.CommandResult{}, fmt.Errorf("%w: %q is destructive and requires payload.confirm=true", gateway.ErrInvalidCommand, cmd.Type)
	}
	body, err := def.build(cmd.Payload)
	if err != nil {
		return gateway.CommandResult{}, fmt.Errorf("%w: %s", gateway.ErrInvalidCommand, err.Error())
	}

	ss := fmt.Sprintf("ctrl_%s_%s", s.serial, hexNow())
	ch := make(chan map[string]any, 1)
	s.pendingMu.Lock()
	s.pending[ss] = ch
	s.pendingMu.Unlock()
	defer func() {
		s.pendingMu.Lock()
		delete(s.pending, ss)
		s.pendingMu.Unlock()
	}()

	if err := s.conn.WriteFrame(buildHowenJSONFrame(def.msgType, mergeBody(ss, body))); err != nil {
		return gateway.CommandResult{}, err
	}

	select {
	case <-ctx.Done():
		return gateway.CommandResult{}, gateway.ErrCommandTimeout
	case resp := <-ch:
		if code := strings.TrimSpace(toString(resp["err"])); code != "" && code != "0" {
			return gateway.CommandResult{}, fmt.Errorf("device rejected command: err=%s", describeHowenError(code))
		}
		return gateway.CommandResult{Data: resp, ReceivedAt: time.Now().UTC()}, nil
	}
}

// resolveDeviceAnswer delivers a DEVICE_ANSWER to a waiting SendCommand by ss.
func (s *session) resolveDeviceAnswer(resp map[string]any) {
	ss := toString(resp["ss"])
	if ss == "" {
		return
	}
	s.pendingMu.Lock()
	ch := s.pending[ss]
	s.pendingMu.Unlock()
	if ch != nil {
		select {
		case ch <- resp:
		default:
		}
	}
}

func mergeBody(ss string, body map[string]any) map[string]any {
	out := map[string]any{"ss": ss}
	for k, v := range body {
		out[k] = v
	}
	return out
}

func isConfirmed(p map[string]any) bool {
	if p == nil {
		return false
	}
	switch v := p["confirm"].(type) {
	case bool:
		return v
	case string:
		s := strings.ToLower(strings.TrimSpace(v))
		return s == "true" || s == "1" || s == "yes"
	default:
		return false
	}
}

func reqStr(p map[string]any, key string) (string, error) {
	if p != nil {
		if v, ok := p[key]; ok && v != nil {
			if s := strings.TrimSpace(toString(v)); s != "" {
				return s, nil
			}
		}
	}
	return "", fmt.Errorf("missing field %q", key)
}

func optStr(p map[string]any, key string) (string, bool) {
	if p != nil {
		if v, ok := p[key]; ok && v != nil {
			if s := strings.TrimSpace(toString(v)); s != "" {
				return s, true
			}
		}
	}
	return "", false
}

package jt808

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"time"

	"github.com/dfm/device-gateway/internal/core/gateway"
)

// config.go implements gateway.ConfigController over the ULV parameter protocol:
// platform request 0xB050 (CmdType Get/Set) and terminal response 0xB051. The
// body of each is a JSON object carrying CmdType + ParamType + the parameter
// fields (ULV spec Table 3.17.4). Ported from jt808Server.js.

// ulvParamTypes is the full set of ULV ParamType segments the N62 exposes
// (docs/jt808/N62_CALL_MAPPING.md). Used when RequestConfig is asked for "all".
// Names are the exact ParamType strings the firmware answers to — an unknown
// name gets no reply (and can briefly wedge the device's config handler), so
// these are verified against the live N62.
var ulvParamTypes = []string{
	"GenDevInfo", "GenDateTime", "GenDst", "GenStartUp", "GenUser",
	"VehBaseInfo", "VehPosition", "VehMileage",
	"PreDisplay", "PreMargin", "PreOsd",
	"RecAttr", "RecStream_M", "RecStream_S", "RecCamAttr", "RecCapAttr",
	"RecPlan", "RecOsd", "RecStorage",
	"AlmIoIn", "AlmSpd", "AlmGsn", "AlmDriving", "AlmSys",
	"NetWired", "NetWifi", "NetXg", "NetCms", "NetFtp", "NetUpload",
	"PerUart", "PerIoOutput",
	"AiBase", "AiAdas", "AiDms", "AiFace",
}

// perParamTimeout bounds one ULV Get/Set round-trip.
const perParamTimeout = 8 * time.Second

// buildUlvParam frames a 0xB050 with a JSON body: type DWORD (0) + JSON length
// DWORD + JSON bytes.
func (s *session) buildUlvParam(obj map[string]any) []byte {
	js, _ := json.Marshal(obj)
	body := make([]byte, 8+len(js))
	binary.BigEndian.PutUint32(body[0:4], 0) // type, always 0
	binary.BigEndian.PutUint32(body[4:8], uint32(len(js)))
	copy(body[8:], js)
	return buildFrame(msgUlvParam, s.phone, s.nextPlatSerial(), body)
}

// parseUlvParamResp decodes a 0xB051 body: reply serial (2) + JSON length (4) +
// JSON. The parsed object is returned (with raw_hex on a JSON parse failure).
func parseUlvParamResp(body []byte) map[string]any {
	if len(body) < 6 {
		return map[string]any{"error": "short 0xB051 body"}
	}
	jlen := int(binary.BigEndian.Uint32(body[2:6]))
	start := 6
	if start+jlen > len(body) || jlen < 0 {
		jlen = len(body) - start
	}
	jsonBytes := body[start : start+jlen]
	var obj map[string]any
	if err := json.Unmarshal(jsonBytes, &obj); err != nil || obj == nil {
		return map[string]any{"raw_text": string(jsonBytes)}
	}
	return obj
}

// RequestConfig reads the named ULV ParamType segments (all of them when modules
// is empty), one round-trip each, and returns them keyed by ParamType.
func (s *session) RequestConfig(ctx context.Context, modules []string) (map[string]any, error) {
	if s.serial == "" || !s.approved {
		return nil, gateway.ErrNotConnected
	}
	wanted := modules
	if len(wanted) == 0 {
		wanted = ulvParamTypes
	}
	out := map[string]any{}
	for _, pt := range wanted {
		rctx, cancel := context.WithTimeout(ctx, perParamTimeout)
		frame := s.buildUlvParam(map[string]any{"CmdType": "Get", "ParamType": pt})
		resp, err := s.request(rctx, msgUlvParamResp, frame)
		cancel()
		if err != nil {
			s.log().Debug(map[string]any{"event": "config_get_failed", "serial": s.serial, "param_type": pt, "error": err.Error()})
			continue // skip a segment the device didn't answer; return the rest
		}
		key := toStr(resp["ParamType"])
		if key == "" {
			key = pt
		}
		out[key] = resp
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("device returned no configuration")
	}
	return out, nil
}

// UpdateConfig writes each changed ULV segment as a Set. sc is keyed by ParamType
// segment; each value is an object of fields to write. Fields are sent verbatim,
// so callers should read-modify-write a segment.
func (s *session) UpdateConfig(ctx context.Context, sc map[string]any) error {
	if s.serial == "" || !s.approved {
		return gateway.ErrNotConnected
	}
	if len(sc) == 0 {
		return fmt.Errorf("no configuration provided")
	}
	for pt, raw := range sc {
		fields, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("config segment %q must be an object", pt)
		}
		obj := map[string]any{"CmdType": "Set", "ParamType": pt}
		for k, v := range fields {
			if k == "CmdType" || k == "ParamType" {
				continue
			}
			obj[k] = v
		}
		rctx, cancel := context.WithTimeout(ctx, perParamTimeout)
		frame := s.buildUlvParam(obj)
		resp, err := s.request(rctx, msgUlvParamResp, frame)
		cancel()
		if err != nil {
			// The device may reboot on a Set without replying; log and continue so a
			// multi-segment update isn't aborted by a silent segment.
			s.log().Debug(map[string]any{"event": "config_set_no_reply", "serial": s.serial, "param_type": pt, "error": err.Error()})
			continue
		}
		s.log().Info(map[string]any{"event": "config_set", "serial": s.serial, "param_type": pt, "result": resp["Result"]})
	}
	return nil
}

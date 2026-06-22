package howen

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// H-Protocol frame constants.
const (
	howenMagic      = 0x48 // 'H'
	howenVersion    = 0x01
	howenHeaderSize = 8
)

// Message types (uint16 LE). Ported from howenCodec.js HOWEN_MESSAGE_TYPES.
const (
	msgHeartbeat              = 0x0001
	msgMediaData              = 0x0011
	msgSignalRegister         = 0x1001
	msgMediaRegister          = 0x1002
	msgLivePreviewResponse    = 0x1010
	msgSignalRegisterResponse = 0x4001
	msgMediaRegisterResponse  = 0x4002
	msgLivePreview            = 0x4010
	msgSnapshot               = 0x4020
	msgSnapshotResponse       = 0x1020
	msgGpsSubscribe           = 0x4040
	msgGpsSubscribeResponse   = 0x1040
	msgGpsStatus              = 0x1041
	msgGpsStatusAck           = 0x4041
	msgAlarmSubscribe         = 0x4050
	msgAlarmSubscribeResponse = 0x1050
	msgAlarmData              = 0x1051
	msgAlarmDataAck           = 0x4051
	msgFileQuery              = 0x4060
	msgFileQueryResponse      = 0x1060
	msgPlayback               = 0x4070
	msgPlaybackResponse       = 0x1070
	msgPlaybackEnd            = 0x1071
	msgParamConfig            = 0x40a0
	msgParamConfigResponse    = 0x10a0
	msgDeviceAnswer           = 0x1100

	// Device control commands (§2.14), each acknowledged by DEVICE_ANSWER (0x1100).
	msgPtzControl       = 0x4100
	msgOutputManage     = 0x4101
	msgRestart          = 0x4102
	msgFactoryReset     = 0x4103
	msgSyncTime         = 0x4104
	msgRecordControl    = 0x4105
	msgClearAlarm       = 0x4106
	msgVehicleControl   = 0x4107
	msgFormatDisk       = 0x4108
	msgGsensorCalibrate = 0x4109
	msgOsdSpeed         = 0x410a
	msgSendMessage      = 0x410b
	msgDeviceLog        = 0x410c
	msgResetMileage     = 0x410d
	msgTtsAudio         = 0x410e
	msgWakeDevice       = 0x410f
)

// ---- frame builders ----

func buildHowenFrame(msgType int, payload []byte) []byte {
	out := make([]byte, howenHeaderSize+len(payload))
	out[0] = howenMagic
	out[1] = howenVersion
	binary.LittleEndian.PutUint16(out[2:4], uint16(msgType))
	binary.LittleEndian.PutUint32(out[4:8], uint32(len(payload)))
	copy(out[8:], payload)
	return out
}

func buildHowenJSONFrame(msgType int, payload any) []byte {
	b, _ := json.Marshal(payload)
	// JS appends "\n\0" after the JSON body.
	body := append(b, '\n', 0)
	return buildHowenFrame(msgType, body)
}

func buildHowenEmptyFrame(msgType int) []byte {
	return buildHowenFrame(msgType, nil)
}

// frameHeader describes a decoded H-Protocol header.
type frameHeader struct {
	Type          int
	PayloadLength int
}

func readHowenFrameHeader(buf []byte) (frameHeader, error) {
	if len(buf) < howenHeaderSize {
		return frameHeader{}, fmt.Errorf("short header")
	}
	if buf[0] != howenMagic {
		return frameHeader{}, fmt.Errorf("unexpected Howen frame magic: 0x%x", buf[0])
	}
	if buf[1] != howenVersion {
		return frameHeader{}, fmt.Errorf("unsupported Howen frame version: %d", buf[1])
	}
	return frameHeader{
		Type:          int(binary.LittleEndian.Uint16(buf[2:4])),
		PayloadLength: int(binary.LittleEndian.Uint32(buf[4:8])),
	}, nil
}

// ---- JSON payload ----

// parseHowenJSONPayload strips the trailing null/newline padding and decodes the
// JSON body into a generic value (object or array).
func parseHowenJSONPayload(payload []byte) (any, error) {
	text := strings.TrimSpace(strings.TrimRight(string(payload), "\x00"))
	if text == "" {
		return map[string]any{}, nil
	}
	var v any
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		return nil, err
	}
	return v, nil
}

func parseHowenJSONObject(payload []byte) (map[string]any, error) {
	v, err := parseHowenJSONPayload(payload)
	if err != nil {
		return nil, err
	}
	if m, ok := v.(map[string]any); ok {
		return m, nil
	}
	return map[string]any{}, nil
}

// ---- binary readers (bounds-checked, mirroring hasBytes/readUIntXX) ----

func hasBytes(buf []byte, off, n int) bool {
	return off >= 0 && n >= 0 && off+n <= len(buf)
}

func readU16(buf []byte, off int) (int, bool) {
	if !hasBytes(buf, off, 2) {
		return 0, false
	}
	return int(binary.LittleEndian.Uint16(buf[off : off+2])), true
}

func readU32(buf []byte, off int) (int64, bool) {
	if !hasBytes(buf, off, 4) {
		return 0, false
	}
	return int64(binary.LittleEndian.Uint32(buf[off : off+4])), true
}

func readI16(buf []byte, off int) (int, bool) {
	if !hasBytes(buf, off, 2) {
		return 0, false
	}
	return int(int16(binary.LittleEndian.Uint16(buf[off : off+2]))), true
}

func readU64(buf []byte, off int) (uint64, bool) {
	if !hasBytes(buf, off, 8) {
		return 0, false
	}
	return binary.LittleEndian.Uint64(buf[off : off+8]), true
}

// parseHowenTime decodes the 6-byte YY(−2000)/MM/DD/HH/MM/SS field to unix
// seconds, returning (0,false) on out-of-range values.
func parseHowenTime(buf []byte, off int) (int64, bool) {
	if !hasBytes(buf, off, 6) {
		return 0, false
	}
	year := 2000 + int(buf[off])
	month := int(buf[off+1])
	day := int(buf[off+2])
	hour := int(buf[off+3])
	minute := int(buf[off+4])
	second := int(buf[off+5])
	if month < 1 || month > 12 || day < 1 || day > 31 || hour > 23 || minute > 59 || second > 59 {
		return 0, false
	}
	return time.Date(year, time.Month(month), day, hour, minute, second, 0, time.UTC).Unix(), true
}

func parseHowenDateTime(value any) (int64, bool) {
	s, ok := value.(string)
	if !ok {
		return 0, false
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	// Accept "YYYY-MM-DD HH:MM:SS" or with 'T'.
	s = strings.Replace(s, "T", " ", 1)
	t, err := time.Parse("2006-01-02 15:04:05", s)
	if err != nil {
		return 0, false
	}
	return t.UTC().Unix(), true
}

func normalizeCoordinateDegree(rawDegree, sign int) int {
	signedDegree := rawDegree
	if rawDegree > 0x7f {
		signedDegree = rawDegree - 0x100
	}
	if sign < 0 && signedDegree < 0 {
		if signedDegree < 0 {
			return -signedDegree
		}
		return signedDegree
	}
	return rawDegree
}

// ---- parsed structures ----

type howenLocation struct {
	Latitude     float64
	Longitude    float64
	Speed        float64
	Altitude     float64
	Accuracy     float64
	Bearing      int
	Satellites   int
	UTC          int64
	HasUTC       bool
	Positioning  int
	LocationType int
}

type howenGsensor struct {
	Identifier            int
	X, Y, Z, Tilt, Impact float64
}

type howenBasicStatus struct {
	Ignition, Brake, TurnLeft, TurnRight, Forward, Reverse int
	LeftFrontDoorOpen, RightFrontDoorOpen, SleepMode       int
}

type howenModuleWorking struct {
	Identifier           int
	MobileNetworkHealth  *int
	LocationModuleHealth *int
	WifiModuleHealth     *int
	GsensorHealth        *int
	RecordingStatusRaw   *int
}

type howenMobileNetwork struct {
	Identifier      int
	SignalIntensity int
	NetworkType     int
}

type howenDisk struct {
	DiskID, Status      int
	SizeMB, RemainingMB int
}

type howenHardDisk struct {
	Identifier int
	Disks      []howenDisk
}

type howenAlarmStatus struct {
	Identifier       int64
	VideoLossMask    *int
	MotionDetectMask *int
	VideoBlindMask   *int
	InputTriggerMask *int
}

type howenEnvironment struct {
	InVehicleTempC  *int
	OutVehicleTempC *int
	MotorTempC      *int
	DeviceTempC     *int
	InVehicleHum    *int
	OutVehicleHum   *int
}

type howenStatusData struct {
	DeviceUTC     int64
	HasDeviceUTC  bool
	ContentBits   int
	Location      *howenLocation
	Gsensor       *howenGsensor
	BasicStatus   *howenBasicStatus
	ModuleWorking *howenModuleWorking
	MobileNetwork *howenMobileNetwork
	HardDisk      *howenHardDisk
	AlarmStatus   *howenAlarmStatus
	Environment   *howenEnvironment
}

func parseHowenLocation(buf []byte, off int) (*howenLocation, int) {
	if !hasBytes(buf, off, 26) {
		return nil, off
	}
	info := int(buf[off])
	locationType := int(buf[off+1])
	utc, hasUtc := parseHowenTime(buf, off+2)
	directionRaw := int(buf[off+8])
	satellites := int(buf[off+9])
	speedRaw, ok1 := readU16(buf, off+10)
	altitudeRaw, ok2 := readU16(buf, off+12)
	accuracyRaw, ok3 := readU16(buf, off+14)
	longitudeDegree := int(buf[off+16])
	longitudeMinutesRaw, ok4 := readU32(buf, off+17)
	latitudeDegree := int(buf[off+21])
	latitudeMinutesRaw, ok5 := readU32(buf, off+22)
	if !ok1 || !ok2 || !ok3 || !ok4 || !ok5 {
		return nil, off
	}
	longitudeMinutes := float64(longitudeMinutesRaw) / 10000
	latitudeMinutes := float64(latitudeMinutesRaw) / 10000
	longitudeSign := 1
	if info&0x02 != 0 {
		longitudeSign = -1
	}
	latitudeSign := 1
	if info&0x10 != 0 {
		latitudeSign = -1
	}
	altitudeSign := 1
	if info&0x04 != 0 {
		altitudeSign = -1
	}
	heading := directionRaw
	if info&0x01 != 0 {
		heading += 180
	}
	normLon := normalizeCoordinateDegree(longitudeDegree, longitudeSign)
	normLat := normalizeCoordinateDegree(latitudeDegree, latitudeSign)
	positioning := 1
	if locationType == 0 {
		positioning = 0
	}
	loc := &howenLocation{
		Latitude:     float64(latitudeSign) * (float64(normLat) + latitudeMinutes/60),
		Longitude:    float64(longitudeSign) * (float64(normLon) + longitudeMinutes/60),
		Speed:        float64(speedRaw) / 100,
		Altitude:     float64(altitudeSign * altitudeRaw),
		Accuracy:     float64(accuracyRaw) / 10,
		Bearing:      heading,
		Satellites:   satellites,
		UTC:          utc,
		HasUTC:       hasUtc,
		Positioning:  positioning,
		LocationType: locationType,
	}
	return loc, off + 26
}

func parseHowenGsensor(buf []byte, off int) (*howenGsensor, int) {
	if !hasBytes(buf, off, 11) {
		return nil, off
	}
	x, _ := readI16(buf, off+1)
	y, _ := readI16(buf, off+3)
	z, _ := readI16(buf, off+5)
	tilt, _ := readI16(buf, off+7)
	impact, _ := readI16(buf, off+9)
	return &howenGsensor{
		Identifier: int(buf[off]),
		X:          float64(x) / 100,
		Y:          float64(y) / 100,
		Z:          float64(z) / 100,
		Tilt:       float64(tilt) / 100,
		Impact:     float64(impact) / 100,
	}, off + 11
}

func parseHowenBasicStatus(buf []byte, off int) (*howenBasicStatus, int) {
	if !hasBytes(buf, off, 4) {
		return nil, off
	}
	f1 := int(buf[off])
	f2 := int(buf[off+1])
	return &howenBasicStatus{
		Ignition:           f1 & 0x01,
		Brake:              (f1 >> 1) & 0x01,
		TurnLeft:           (f1 >> 2) & 0x01,
		TurnRight:          (f1 >> 3) & 0x01,
		Forward:            (f1 >> 4) & 0x01,
		Reverse:            (f1 >> 5) & 0x01,
		LeftFrontDoorOpen:  (f1 >> 6) & 0x01,
		RightFrontDoorOpen: (f1 >> 7) & 0x01,
		SleepMode:          (f2 >> 7) & 0x01,
	}, off + 4
}

func parseHowenModuleWorking(buf []byte, off int) (*howenModuleWorking, int) {
	identifier, ok := readU16(buf, off)
	if !ok {
		return nil, off
	}
	o := off + 2
	mw := &howenModuleWorking{Identifier: identifier}
	readByte := func(dst **int) bool {
		if !hasBytes(buf, o, 1) {
			return false
		}
		v := int(buf[o])
		*dst = &v
		o++
		return true
	}
	if identifier&0x0001 != 0 {
		if !readByte(&mw.MobileNetworkHealth) {
			return mw, o
		}
	}
	if identifier&0x0002 != 0 {
		if !readByte(&mw.LocationModuleHealth) {
			return mw, o
		}
	}
	if identifier&0x0004 != 0 {
		if !readByte(&mw.WifiModuleHealth) {
			return mw, o
		}
	}
	if identifier&0x0008 != 0 {
		if !readByte(&mw.GsensorHealth) {
			return mw, o
		}
	}
	if identifier&0x0010 != 0 {
		if v, ok := readU16(buf, o); ok {
			mw.RecordingStatusRaw = &v
			o += 2
		} else {
			return mw, o
		}
	}
	return mw, o
}

func parseHowenFuelConsumption(buf []byte, off int) int {
	// JS only advances the offset; fields are not surfaced into the payload.
	if !hasBytes(buf, off, 1) {
		return off
	}
	identifier := int(buf[off])
	o := off + 1
	if identifier&0x01 != 0 {
		if hasBytes(buf, o, 2) {
			o += 2
		} else {
			return o
		}
	}
	if identifier&0x02 != 0 {
		if hasBytes(buf, o, 2) {
			o += 2
		} else {
			return o
		}
	}
	return o
}

func parseHowenMobileNetwork(buf []byte, off int) (*howenMobileNetwork, int) {
	if !hasBytes(buf, off, 5) {
		return nil, off
	}
	return &howenMobileNetwork{
		Identifier:      int(buf[off]),
		SignalIntensity: int(buf[off+1]),
		NetworkType:     int(buf[off+2]),
	}, off + 5
}

func parseHowenHardDisk(buf []byte, off int) (*howenHardDisk, int) {
	if !hasBytes(buf, off, 1) {
		return nil, off
	}
	identifier := int(buf[off])
	o := off + 1
	hd := &howenHardDisk{Identifier: identifier, Disks: []howenDisk{}}
	for bit := 0; bit < 8; bit++ {
		if identifier&(1<<bit) == 0 {
			continue
		}
		if !hasBytes(buf, o, 10) {
			return hd, o
		}
		sizeMb, _ := readU32(buf, o+2)
		remMb, _ := readU32(buf, o+6)
		hd.Disks = append(hd.Disks, howenDisk{
			DiskID:      int(buf[o]),
			Status:      int(buf[o+1]),
			SizeMB:      int(sizeMb),
			RemainingMB: int(remMb),
		})
		o += 10
	}
	return hd, o
}

func parseHowenAlarmStatus(buf []byte, off int) (*howenAlarmStatus, int) {
	identifier, ok := readU32(buf, off)
	if !ok {
		return nil, off
	}
	o := off + 4
	as := &howenAlarmStatus{Identifier: identifier}
	readMask := func(dst **int) bool {
		if v, ok := readU16(buf, o); ok {
			*dst = &v
			o += 2
			return true
		}
		return false
	}
	if identifier&0x00000001 != 0 {
		if !readMask(&as.VideoLossMask) {
			return as, o
		}
	}
	if identifier&0x00000002 != 0 {
		if !readMask(&as.MotionDetectMask) {
			return as, o
		}
	}
	if identifier&0x00000004 != 0 {
		if !readMask(&as.VideoBlindMask) {
			return as, o
		}
	}
	if identifier&0x00000008 != 0 {
		if !readMask(&as.InputTriggerMask) {
			return as, o
		}
	}
	return as, o
}

func parseHowenEnvironment(buf []byte, off int) (*howenEnvironment, int) {
	identifier, ok := readU16(buf, off)
	if !ok {
		return nil, off
	}
	o := off + 2
	env := &howenEnvironment{}
	readTemp := func(dst **int) bool {
		if v, ok := readI16(buf, o); ok {
			*dst = &v
			o += 2
			return true
		}
		return false
	}
	if identifier&0x0001 != 0 {
		if !readTemp(&env.InVehicleTempC) {
			return env, o
		}
	}
	if identifier&0x0002 != 0 {
		if !readTemp(&env.OutVehicleTempC) {
			return env, o
		}
	}
	if identifier&0x0004 != 0 {
		if !readTemp(&env.MotorTempC) {
			return env, o
		}
	}
	if identifier&0x0008 != 0 {
		if !readTemp(&env.DeviceTempC) {
			return env, o
		}
	}
	if identifier&0x0010 != 0 {
		if !hasBytes(buf, o, 1) {
			return env, o
		}
		v := int(buf[o])
		env.InVehicleHum = &v
		o++
	}
	if identifier&0x0020 != 0 {
		if !hasBytes(buf, o, 1) {
			return env, o
		}
		v := int(buf[o])
		env.OutVehicleHum = &v
		o++
	}
	return env, o
}

// parseHowenStatusData ports the bitmask-driven status decoder.
func parseHowenStatusData(buf []byte) *howenStatusData {
	if len(buf) < 8 {
		return nil
	}
	deviceUtc, hasUtc := parseHowenTime(buf, 0)
	contentBits, ok := readU16(buf, 6)
	if !ok {
		return nil
	}
	sd := &howenStatusData{DeviceUTC: deviceUtc, HasDeviceUTC: hasUtc, ContentBits: contentBits}
	off := 8

	if contentBits&0x0001 != 0 {
		sd.Location, off = parseHowenLocation(buf, off)
	}
	if contentBits&0x0002 != 0 {
		sd.Gsensor, off = parseHowenGsensor(buf, off)
	}
	if contentBits&0x0004 != 0 {
		sd.BasicStatus, off = parseHowenBasicStatus(buf, off)
	}
	if contentBits&0x0008 != 0 {
		sd.ModuleWorking, off = parseHowenModuleWorking(buf, off)
	}
	if contentBits&0x0010 != 0 {
		off = parseHowenFuelConsumption(buf, off)
	}
	if contentBits&0x0020 != 0 {
		sd.MobileNetwork, off = parseHowenMobileNetwork(buf, off)
	}
	// bit6 (Wi-Fi) not decoded; the JS parser skips disk/alarm/env when it is set.
	if contentBits&0x0040 == 0 {
		if contentBits&0x0080 != 0 {
			sd.HardDisk, off = parseHowenHardDisk(buf, off)
		}
		if contentBits&0x0100 != 0 {
			sd.AlarmStatus, off = parseHowenAlarmStatus(buf, off)
		}
		if contentBits&0x0200 != 0 {
			sd.Environment, off = parseHowenEnvironment(buf, off)
		}
	}
	_ = off
	return sd
}

// statusPayload is a decoded GPS_STATUS (0x1041) packet.
type statusPayload struct {
	Session string
	Status  *howenStatusData
}

func parseHowenStatusPayload(payload []byte) *statusPayload {
	if len(payload) < 1 {
		return nil
	}
	sessionLen := int(payload[0])
	if sessionLen < 1 || len(payload) < 1+sessionLen+8 {
		return nil
	}
	session := strings.TrimRight(string(payload[1:1+sessionLen]), "\x00")
	sd := parseHowenStatusData(payload[1+sessionLen:])
	if sd == nil {
		return nil
	}
	return &statusPayload{Session: session, Status: sd}
}

// alarmPayload is a decoded ALARM_DATA (0x1051) packet.
type alarmPayload struct {
	Session       string
	Alarm         map[string]any
	AlarmRaw      any // may be object or array for some events
	Detail        map[string]any
	DetailRaw     any
	EC            any // numeric (float64) or nil
	EventCodes    []string
	EventStartUTC *int64
	EventEndUTC   *int64
	DeviceUTC     *int64
	Status        *howenStatusData
}

func parseHowenAlarmPayload(payload []byte) *alarmPayload {
	if len(payload) < 6 {
		return nil
	}
	sessionLen := int(payload[0])
	if sessionLen < 1 || len(payload) < 1+sessionLen+4 {
		return nil
	}
	sessionEnd := 1 + sessionLen
	session := strings.TrimRight(string(payload[1:sessionEnd]), "\x00")
	jsonLen64, ok := readU32(payload, sessionEnd)
	if !ok {
		return nil
	}
	jsonLen := int(jsonLen64)
	if jsonLen < 1 || len(payload) < sessionEnd+4+jsonLen {
		return nil
	}
	jsonStart := sessionEnd + 4
	alarmRaw, err := parseHowenJSONPayload(payload[jsonStart : jsonStart+jsonLen])
	if err != nil {
		return nil
	}
	alarmObj, _ := alarmRaw.(map[string]any)
	var detailRaw any
	var detailObj map[string]any
	if alarmObj != nil {
		detailRaw = alarmObj["det"]
		detailObj, _ = detailRaw.(map[string]any)
	}

	var ec any
	if alarmObj != nil {
		if v, ok := numberOrNullInt(alarmObj["ec"]); ok {
			ec = float64(v)
		}
	}

	statusBuf := payload[jsonStart+jsonLen:]
	var status *howenStatusData
	if len(statusBuf) > 0 {
		status = parseHowenStatusData(statusBuf)
	}

	ap := &alarmPayload{
		Session:    session,
		Alarm:      alarmObj,
		AlarmRaw:   alarmRaw,
		Detail:     detailObj,
		DetailRaw:  detailRaw,
		EC:         ec,
		EventCodes: mapHowenEventCodes(ec, detailObj, alarmObj),
		Status:     status,
	}
	if alarmObj != nil {
		if v, ok := parseHowenDateTime(alarmObj["st"]); ok {
			ap.EventStartUTC = &v
		}
		if v, ok := parseHowenDateTime(alarmObj["et"]); ok {
			ap.EventEndUTC = &v
		}
		if v, ok := parseHowenDateTime(alarmObj["dtu"]); ok {
			ap.DeviceUTC = &v
		}
	}
	return ap
}

// describeHowenError renders a Howen err code as "code (meaning)".
func describeHowenError(code any) string {
	meanings := map[string]string{
		"0": "success", "1": "duplicated id", "2": "invalid parameter",
		"3": "invalid command", "4": "device busy", "5": "connection lost",
		"6": "related file does not exist", "7": "disk does not exist",
		"8": "follow-up data (more records coming)", "9": "file search finished",
		"10": "device not authorized", "15": "access denied", "255": "unknown error",
	}
	norm := strings.TrimSpace(toString(code))
	if norm == "" {
		norm = "unknown"
	}
	meaning := meanings[norm]
	if meaning == "" {
		meaning = "unrecognized error code"
	}
	return norm + " (" + meaning + ")"
}

func toString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case int:
		return strconv.Itoa(t)
	default:
		return fmt.Sprintf("%v", t)
	}
}

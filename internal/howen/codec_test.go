package howen

import (
	"encoding/hex"
	"math"
	"testing"
)

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("bad hex: %v", err)
	}
	return b
}

func approx(a, b float64) bool { return math.Abs(a-b) < 0.00001 }

// Live on-the-wire alarm payloads captured from real Howen units (lifted verbatim
// from the JS parser-test.js golden vectors).
const (
	liveVoltageAlarmHex       = "22616c61726d2d3836343331323038373834353331332d313945323737333538324500d00000007b22646574223a7b22637572223a2231323330222c226474223a2231222c227674223a2231313530227d2c2264726964223a22222c2264726e616d65223a22222c22647475223a22323032362d30352d31342031373a30353a3533222c226563223a223238222c226574223a22222c2266656e63656964223a22222c2273706473223a2230222c227374223a22323032362d30352d31342031373a30353a3533222c2275756964223a2231373738373835353533303930303030303130383634333132303837383435333133227d0a001a050e110535050011011a050e110535040e00007301080014f79a0600dff4400800012000000000"
	liveZeroVoltageAlarmHex   = "1b616c61726d2d38373834353331332d313945383332463535463700ca0000007b22646574223a7b22637572223a2230222c226474223a2237222c227674223a2230227d2c2264726964223a22222c2264726e616d65223a22222c22647475223a22323032362d30362d30312031323a33353a3534222c226563223a223238222c226574223a22222c2266656e63656964223a22222c2273706473223a2230222c227374223a22323032362d30362d30312031323a33353a3534222c2275756964223a2231373830333234353534303930303030303530303030303030303837383435333133227d0a001a06010c2411ad0011011a06010c2336b015000080010600145c9b0600dfe2400800010000001f000301000103000000000000010f0156dc0100000000000000"
	liveIgnitionOnAlarmHex    = "22616c61726d2d3836343331323038373834353331332d313945334634303535443700c40000007b22646574223a6e756c6c2c2264726964223a22222c2264726e616d65223a22222c22647475223a22323032362d30352d31392030383a30383a3136222c226563223a223331222c226574223a22323032362d30352d31392030383a30383a3136222c2266656e63656964223a22222c2273706473223a2230222c227374223a22323032362d30352d31392030383a30383a3136222c2275756964223a2231373739313835323936303830303030303130383634333132303837383435333133227d0a001a0513080810250010011a05130808101006000070010e0014559b0600dfcd4008000120000000000000000000"
	liveIgnitionOffAlarmHex   = "22616c61726d2d3836343331323038373834353331332d313945334634303535443700c40000007b22646574223a6e756c6c2c2264726964223a22222c2264726e616d65223a22222c22647475223a22323032362d30352d31392030383a31313a3135222c226563223a223139222c226574223a22323032362d30352d31392030383a31313a3135222c2266656e63656964223a22222c2273706473223a2230222c227374223a22323032362d30352d31392030383a31313a3135222c2275756964223a2231373739313835343735303830303030303130383634333132303837383435333133227d0a001a0513080b0f250010011a0513080b0f100b00007001080014559b0600dfcd4008000020000000000000000000"
	liveDmsCellphoneAlarmHex  = "22616c61726d2d3836343331323038373834353331332d313945334637424139354100dd0000007b22646574223a7b226964223a22222c226e616d65223a22222c227470223a223334227d2c2264726964223a22222c2264726e616d65223a22222c22647475223a22323032362d30352d31392030393a33353a3435222c226563223a223330222c226574223a22323032362d30352d31392030393a33353a3435222c2266656e63656964223a22222c2273706473223a2230222c227374223a22323032362d30352d31392030393a33353a3435222c2275756964223a2231373739313930353435333430303030303030383634333132303837383435333133227d0a001a051309232d2d0010011a051309232d101500007001050014559b0600dfcd400800012000001f0000010101030000000000000000"
	liveDiskDetectionAlarmHex = "1b616c61726d2d38373834353331332d313945364545314138343100b50000007b22646574223a5b7b226e756d223a22736431222c226f77223a307d5d2c2264726964223a22222c2264726e616d65223a22222c22647475223a22323032362d30352d32382031343a30303a3430222c226563223a22373730222c226574223a22323032362d30352d32382031343a30303a3430222c2266656e63656964223a22222c2273706473223a2230222c227374223a22323032362d30352d32382031343a30303a3430222c2275756964223a22227d0a001a051c0e00282d0010011a051c0e00282c1700007501060014429b0600dfee400800014000001f0001010001030000060500000000"
)

func TestParseLiveVoltageAlarm(t *testing.T) {
	ap := parseHowenAlarmPayload(mustHex(t, liveVoltageAlarmHex))
	if ap == nil {
		t.Fatal("nil parse")
	}
	if got := mapHowenEventCodes("", ap.EC, ap.Detail, ap.Alarm)[0]; got != "BATTERY:LOW:EXT" {
		t.Fatalf("event = %q, want BATTERY:LOW:EXT", got)
	}
	if ap.Status == nil || ap.Status.Location == nil {
		t.Fatal("missing status location")
	}
	if !approx(ap.Status.Location.Latitude, -33.901526666666666) {
		t.Fatalf("lat = %v", ap.Status.Location.Latitude)
	}
	if !approx(ap.Status.Location.Longitude, 20.721478333333334) {
		t.Fatalf("lon = %v", ap.Status.Location.Longitude)
	}
}

func TestParseLiveZeroVoltageAlarm(t *testing.T) {
	ap := parseHowenAlarmPayload(mustHex(t, liveZeroVoltageAlarmHex))
	if ap == nil {
		t.Fatal("nil parse")
	}
	// Live capture: voltage alarm dt=7. Per H-Protocol spec §14, dt=7 is
	// "Start up" (power-on), not a generic abnormality — see
	// docs/Howen_mapping_improvements.md.
	if dt, _ := ap.Detail["dt"].(string); dt != "7" {
		t.Fatalf("detail.dt = %q, want 7", dt)
	}
	if got := mapHowenEventCodes("", ap.EC, ap.Detail, ap.Alarm)[0]; got != "POWER:ON" {
		t.Fatalf("event = %q, want POWER:ON", got)
	}
}

func TestParseLiveIgnitionOnAlarm(t *testing.T) {
	ap := parseHowenAlarmPayload(mustHex(t, liveIgnitionOnAlarmHex))
	if ap == nil || ap.EC == nil || ap.EC.(float64) != 31 {
		t.Fatalf("ec = %v, want 31", ap.EC)
	}
	if mapHowenEventCodes("", ap.EC, ap.Detail, ap.Alarm)[0] != "IGNITION:ON" {
		t.Fatalf("event = %q", mapHowenEventCodes("", ap.EC, ap.Detail, ap.Alarm)[0])
	}
	if ap.Status == nil || ap.Status.BasicStatus == nil || ap.Status.BasicStatus.Ignition != 1 {
		t.Fatal("ignition should be 1")
	}
}

func TestParseLiveIgnitionOffAlarm(t *testing.T) {
	ap := parseHowenAlarmPayload(mustHex(t, liveIgnitionOffAlarmHex))
	if ap == nil || ap.EC == nil || ap.EC.(float64) != 19 {
		t.Fatalf("ec = %v, want 19", ap.EC)
	}
	if mapHowenEventCodes("", ap.EC, ap.Detail, ap.Alarm)[0] != "IGNITION:OFF" {
		t.Fatalf("event = %q", mapHowenEventCodes("", ap.EC, ap.Detail, ap.Alarm)[0])
	}
	if ap.Status.BasicStatus.Ignition != 0 {
		t.Fatal("ignition should be 0")
	}
}

func TestParseLiveDmsCellphoneAlarm(t *testing.T) {
	ap := parseHowenAlarmPayload(mustHex(t, liveDmsCellphoneAlarmHex))
	if ap == nil || ap.EC.(float64) != 30 {
		t.Fatalf("ec = %v", ap.EC)
	}
	if tp, _ := ap.Detail["tp"].(string); tp != "34" {
		t.Fatalf("detail.tp = %q", tp)
	}
	if mapHowenEventCodes("", ap.EC, ap.Detail, ap.Alarm)[0] != "AI:CELLPHONE" {
		t.Fatalf("event = %q", mapHowenEventCodes("", ap.EC, ap.Detail, ap.Alarm)[0])
	}
}

func TestParseLiveDiskDetectionAlarm(t *testing.T) {
	ap := parseHowenAlarmPayload(mustHex(t, liveDiskDetectionAlarmHex))
	if ap == nil || ap.EC.(float64) != 770 {
		t.Fatalf("ec = %v, want 770", ap.EC)
	}
	arr, ok := ap.DetailRaw.([]any)
	if !ok || len(arr) == 0 {
		t.Fatalf("detail should be array: %T", ap.DetailRaw)
	}
	first, _ := arr[0].(map[string]any)
	if first["num"] != "sd1" {
		t.Fatalf("detail[0].num = %v", first["num"])
	}
	if mapHowenEventCodes("", ap.EC, ap.Detail, ap.Alarm)[0] != "ALARM:HARDWARE:FAULT" {
		t.Fatalf("event = %q", mapHowenEventCodes("", ap.EC, ap.Detail, ap.Alarm)[0])
	}
}

func TestFrameRoundTrip(t *testing.T) {
	frame := buildHowenJSONFrame(msgSignalRegister, map[string]any{"dn": "10011", "imei": "865847053306518"})
	h, err := readHowenFrameHeader(frame)
	if err != nil {
		t.Fatal(err)
	}
	if h.Type != msgSignalRegister {
		t.Fatalf("type = %x", h.Type)
	}
	if h.PayloadLength != len(frame)-8 {
		t.Fatalf("len = %d", h.PayloadLength)
	}
	obj, err := parseHowenJSONObject(frame[8:])
	if err != nil {
		t.Fatal(err)
	}
	if obj["dn"] != "10011" {
		t.Fatalf("dn = %v", obj["dn"])
	}

	hb := buildHowenEmptyFrame(msgHeartbeat)
	hh, _ := readHowenFrameHeader(hb)
	if hh.Type != msgHeartbeat || hh.PayloadLength != 0 {
		t.Fatalf("heartbeat header wrong: %+v", hh)
	}
}

func TestMobileNetworkMapping(t *testing.T) {
	mw := &howenModuleWorking{Identifier: 0x0001}
	health := 1
	mw.MobileNetworkHealth = &health
	mn := &howenMobileNetwork{SignalIntensity: 8, NetworkType: 5}
	out := howenMobileNetworkToPayloadFields(mn, mw)
	if out["signal_lvl"].(float64) != 8 {
		t.Fatalf("signal_lvl = %v", out["signal_lvl"])
	}
	if out["signal_str"].(float64) != 80 {
		t.Fatalf("signal_str = %v", out["signal_str"])
	}
	if out["data_mode"].(string) != "5" {
		t.Fatalf("data_mode = %v", out["data_mode"])
	}
	status := out["gsm_status"].([]any)
	want := map[string]bool{"howen_network_type:5": false, "howen_radio:4g": false, "howen_modem_health:normal": false}
	for _, s := range status {
		if _, ok := want[s.(string)]; ok {
			want[s.(string)] = true
		}
	}
	for k, seen := range want {
		if !seen {
			t.Fatalf("missing gsm_status token %q in %v", k, status)
		}
	}

	zero := howenMobileNetworkToPayloadFields(&howenMobileNetwork{SignalIntensity: 0, NetworkType: 0}, nil)
	if zero["signal_lvl"].(float64) != 0 {
		t.Fatalf("zero signal_lvl = %v", zero["signal_lvl"])
	}
	if _, ok := zero["signal_str"]; ok {
		t.Fatal("signal_str should be omitted for invalid signal")
	}
	if !containsAny(zero["gsm_status"].([]any), "howen_signal:invalid") {
		t.Fatal("missing howen_signal:invalid")
	}
}

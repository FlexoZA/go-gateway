package howen

import "testing"

func TestMapHowenEventCodes(t *testing.T) {
	cases := []struct {
		name   string
		code   any
		detail map[string]any
		alarm  map[string]any
		want   string
	}{
		{"video loss channel", 1, map[string]any{"ch": "4"}, nil, "VIDEO:SIGNAL_LOSS:CHANNEL:4"},
		{"emergency input panic", 5, map[string]any{"num": "1"}, nil, "PANIC"},
		{"overspeed start", 7, nil, map[string]any{"et": ""}, "SPEEDING:START"},
		{"overspeed end", 7, nil, map[string]any{"et": "2026-03-14 08:35:00"}, "SPEEDING:END"},
		{"vibration 1", 12, map[string]any{"dt": "1"}, nil, "ALARM:VIBRATION"},
		{"vibration 2", 12, map[string]any{"dt": "2"}, nil, "ALARM:VIBRATION"},
		{"vibration 3", 12, map[string]any{"dt": "3"}, nil, "ALARM:VIBRATION"},
		{"collision", 12, map[string]any{"dt": "4"}, nil, "COLLISION"},
		{"turn over", 12, map[string]any{"dt": "5"}, nil, "COLLISION:TURN_OVER"},
		{"cornering", 12, map[string]any{"dt": "6"}, nil, "HARSH:CORNERING"},
		{"accel", 12, map[string]any{"dt": "7"}, nil, "HARSH:ACCELERATION"},
		{"braking", 12, map[string]any{"dt": "8"}, nil, "HARSH:BRAKING"},
		{"geofence enter", 13, map[string]any{"st": "0"}, nil, "ZONE:ENTER"},
		{"door open", 15, map[string]any{"st": "1"}, nil, "ALARM:DOOR_OPEN"},
		{"door close", 15, map[string]any{"st": "0"}, nil, "INPUT:DOOR_CLOSE"},
		{"swipe card driver", 22, map[string]any{"tp": "1"}, nil, "CARD:DRIVER"},
		{"swipe card invalid", 22, map[string]any{"tp": "3"}, nil, "CARD:INVALID"},
		{"ignition off", 19, nil, nil, "IGNITION:OFF"},
		{"low speed", 26, nil, nil, "SPEED:LOW"},
		{"battery disc", 28, map[string]any{"dt": "3"}, nil, "BATTERY:DISCONNECTED"},
		{"battery disc moving", 28, map[string]any{"dt": "5"}, nil, "BATTERY:DISCONNECTED"},
		{"battery disc caps", 28, map[string]any{"DT": "3"}, nil, "BATTERY:DISCONNECTED"},
		{"dms fatigue", 30, map[string]any{"tp": "33"}, nil, "AI:FATIGUE"},
		{"dms cellphone", 30, map[string]any{"tp": "34"}, nil, "AI:CELLPHONE"},
		{"dms smoking", 30, map[string]any{"tp": "35"}, nil, "AI:SMOKING"},
		{"dms driver abnormal", 30, map[string]any{"tp": "37"}, nil, "AI:DRIVER:ABNORMAL"},
		{"dms eyes closed crit", 30, map[string]any{"tp": "39"}, nil, "AI:EYES_CLOSED:CRITICAL"},
		{"dms yawn crit", 30, map[string]any{"tp": "40"}, nil, "AI:YAWN:CRITICAL"},
		{"dms driver change", 30, map[string]any{"tp": "49"}, nil, "AI:DRIVER:CHANGE"},
		{"dms eyes closed", 30, map[string]any{"tp": "65"}, nil, "AI:EYES_CLOSED"},
		{"dms yawn", 30, map[string]any{"tp": "66"}, nil, "AI:YAWN"},
		{"dms eyes fail", 30, map[string]any{"tp": "80"}, nil, "AI:EYES_DETECTION_FAILED"},
		{"dms driver mask", 30, map[string]any{"tp": "85"}, nil, "AI:DRIVER:MASK"},
		{"adas blindspot lo", 30, map[string]any{"tp": "96"}, nil, "AI:BLINDSPOT"},
		{"adas blindspot hi", 30, map[string]any{"tp": "107"}, nil, "AI:BLINDSPOT"},
		{"dms seatbelt", 30, map[string]any{"tp": "69"}, nil, "AI:SEATBELT"},
		{"dms no driver", 30, map[string]any{"tp": "70"}, nil, "AI:NO_DRIVER"},
		{"ignition on", 31, nil, nil, "IGNITION:ON"},
		{"trip start", 41, nil, nil, "TRIP:START"},
		{"trip end 43", 43, nil, nil, "TRIP:END"},
		{"trip end 768", 768, nil, nil, "TRIP:END"},
		{"water flow", 773, nil, nil, "WATER:FLOW"},
		{"fatigue 62", 62, nil, nil, "AI:FATIGUE"},
		{"media lane dep", 1282, map[string]any{"fn": "/mnt/sd1/REC-ALARM/20260521/115849_2/0_65_2_0_1779364729.mp4"}, nil, "AI:LANE_DEPARTURE"},
		{"media follow dist", 1282, map[string]any{"fn": "/mnt/sd1/REC-ALARM/20260528/133350_18/0_65_18_2_1779975230.mp4"}, nil, "FOLLOWING:DISTANCE:VIOLATION"},
		{"media smoking", 1282, map[string]any{"fn": "/mnt/sd1/REC-ALARM/20260513/115828_35/1_64_35_1_1778673508.mp4"}, nil, "AI:SMOKING"},
		{"media eyes crit", 1282, map[string]any{"fn": "/mnt/sd1/REC-ALARM/20260519/115316_39/1_64_39_10_1779191596.mp4"}, nil, "AI:EYES_CLOSED:CRITICAL"},
		{"media yawn crit", 1282, map[string]any{"fn": "/mnt/sd1/REC-ALARM/20260519/120411_40/1_64_40_11_1779192251.mp4"}, nil, "AI:YAWN:CRITICAL"},
		{"media eyes closed jpg", 1282, map[string]any{"fn": "/mnt/sd1/REC-ALARM/20260513/114334_65/1_64_65_2_1778672614_0.jpg"}, nil, "AI:EYES_CLOSED"},
		{"media yawn jpg", 1282, map[string]any{"fn": "/mnt/sd1/REC-ALARM/20260519/114026_66/1_64_66_3_1779190826_0.jpg"}, nil, "AI:YAWN"},
		{"media lens covered", 1282, map[string]any{"fn": "/mnt/sd1/REC-ALARM/20260519/121333_67/1_64_67_7_1779192813_0.jpg"}, nil, "AI:LENS_COVERED"},
		{"media distraction", 1282, map[string]any{"fn": "/mnt/sd1/REC-ALARM/20260513/113500_68/1_64_68_8_1778672100.mp4"}, nil, "AI:DISTRACTION"},
		{"media seatbelt", 1282, map[string]any{"fn": "/mnt/sd1/REC-ALARM/20260513/110251_69/1_64_69_8_1778670171.mp4"}, nil, "AI:SEATBELT"},
		{"media no driver", 1282, map[string]any{"fn": "/mnt/sd1/REC-ALARM/20260513/092421_70/1_64_70_5_1778664261.mp4"}, nil, "AI:NO_DRIVER"},
		{"media eyes fail", 1282, map[string]any{"fn": "/mnt/sd1/REC-ALARM/20260519/112907_80/1_64_80_6_1779190147.mp4"}, nil, "AI:EYES_DETECTION_FAILED"},
		{"unknown", 9999, nil, nil, "ALARM"},

		// --- corrected/added mappings (docs/Howen_mapping_improvements.md) ---
		{"input door close 14", 4, map[string]any{"num": "14"}, nil, "INPUT:DOOR_CLOSE"},
		{"input door close 16", 4, map[string]any{"num": "16"}, nil, "INPUT:DOOR_CLOSE"},
		{"input turn right", 4, map[string]any{"num": "9"}, nil, "INPUT:TURN_RIGHT"},
		{"input reverse", 4, map[string]any{"num": "12"}, nil, "INPUT:REVERSE"},
		{"input low beam", 4, map[string]any{"num": "5"}, nil, "INPUT:LOW_BEAM"},
		{"geofence low speed", 13, map[string]any{"st": "4"}, nil, "ZONE:SPEED_LOW"},
		{"geofence pre-entry", 13, map[string]any{"st": "9"}, nil, "ZONE:PRE_ENTER"},
		{"voltage high", 28, map[string]any{"dt": "2"}, nil, "BATTERY:HIGH:EXT"},
		{"voltage startup", 28, map[string]any{"dt": "7"}, nil, "POWER:ON"},
		{"voltage abnormal shutdown", 28, map[string]any{"dt": "6"}, nil, "POWER:OFF:ABNORMAL"},
		{"dms auth fail", 30, map[string]any{"tp": "82"}, nil, "AI:AUTH:FAIL"},
		{"dms drinking", 30, map[string]any{"tp": "71"}, nil, "AI:DRINKING"},
		{"dms eating", 30, map[string]any{"tp": "87"}, nil, "AI:EATING"},
		{"motion detection", 2, nil, nil, "ALARM:MOTION"},
		{"low speed alarm", 6, nil, nil, "SPEED:LOW"},
		{"adas calibrate", 80, nil, nil, "AI:CALIBRATION"},
		{"ai install abnormal", 78, nil, nil, "AI:INSTALL:ABNORMAL"},
		{"alcohol", 61, nil, nil, "ALARM:ALCOHOL"},
		{"satellite modem", 60, nil, nil, "SATELLITE:ABNORMAL"},
		{"tire pressure", 769, nil, nil, "TIRE:PRESSURE"},
		{"people count", 29, nil, nil, "PEOPLE:COUNT"},
		{"self check", 772, nil, nil, "DEVICE:SELF_CHECK"},
		{"gps antenna break", 33, nil, nil, "GPS:ANTENNA:BREAK"},
		{"sim lost", 50, nil, nil, "SIM:LOST"},
		{"load alarm", 49, nil, nil, "LOAD:ALARM"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mapHowenEventCodes("", tc.code, tc.detail, tc.alarm)
			if got[0] != tc.want {
				t.Fatalf("got %q, want %q", got[0], tc.want)
			}
		})
	}
}

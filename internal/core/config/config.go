// Package config loads gateway configuration from environment variables.
//
// One gateway binary serves exactly one unit type, so configuration is flat and
// env-driven (no per-protocol branching). A protocol plugin reads the generic
// fields it needs and may read its own extra vars via os.Getenv.
package config

import (
	"os"
	"strconv"
	"strings"
)

// Config holds the runtime configuration shared by the framework core.
type Config struct {
	// Gateway is a human identifier for this gateway instance, surfaced in the
	// universal message "gateway" field.
	Gateway string

	// ListenHost / ListenPort is where the device TCP server binds.
	ListenHost string
	ListenPort int

	// HTTPPort is the management/control HTTP API port (API-key protected). 0
	// disables the HTTP API. Binds on ListenHost.
	HTTPPort int

	// InternalAPIToken is a shared secret the admin panel (BFF) uses to
	// authenticate to the HTTP API, accepted alongside DB-minted API keys. It
	// lets the panel work before any key exists (first-run setup) and keeps the
	// panel's own access separate from user-facing external API keys. Empty
	// disables it.
	InternalAPIToken string

	// DatabaseURL is the PostgreSQL connection string — the primary data sink.
	// Empty disables Postgres storage (and forces allow_all device auth).
	DatabaseURL string

	// WebhookURL is the optional universal JSON HTTP sink (legacy DB/N8N). Empty
	// disables webhook delivery; Postgres remains the primary store.
	WebhookURL string

	// WebhookTimezoneOffsetHours controls the offset embedded in message
	// timestamps. The JS gateway uses 0 (UTC).
	WebhookTimezoneOffsetHours float64

	// WebhookOutboxMax caps the durable webhook delivery queue (the on-DB spool that
	// buffers telemetry through a webhook outage). Beyond it, the OLDEST undelivered
	// messages are dropped so a long outage can't grow the DB without bound. Applies
	// only when a database is configured; 0 disables the cap (unbounded). Default
	// 200000 (~hours of buffering at moderate rates).
	WebhookOutboxMax int

	// DeviceTZOffsetHours is the device's local-clock offset from UTC (e.g. +2
	// for SAST). Howen indexes recordings by LOCAL wall-clock, so clip playback
	// requests (0x4070) must localize the start/end window by this offset or the
	// device returns err=6 (file not found). Clip times in our DB/API stay UTC.
	DeviceTZOffsetHours float64

	// DeviceAuthMode selects how connecting devices are authorized:
	//   "allow_all" — accept every device, no registry.
	//   "postgres"  — track devices in the PostgreSQL registry.
	// Defaults to "postgres" when DatabaseURL is set, else "allow_all".
	DeviceAuthMode string

	// DeviceRejectUnknown, when true and DeviceAuthMode is "postgres", quarantines
	// and rejects serials not already present in the devices table until approved.
	// Default true: unknown devices require approval (secure by default). Set false
	// to auto-provision and admit every serial.
	DeviceRejectUnknown bool

	// MappingRefreshSeconds is how often the gateway reloads front-end-edited
	// event mappings from the database. 0 disables periodic refresh (mappings are
	// still loaded once at startup). Default 60.
	MappingRefreshSeconds int

	// --- Video / live media (HLS) ---

	// MediaPort is the TCP port devices connect to for media (video) frames,
	// separate from the control port. Default 33001.
	MediaPort int

	// MediaAdvertiseHost is the host (no port) that the device dials back for
	// media — embedded in the live-preview "srv" field as <host>:<MediaPort>.
	// Empty DISABLES video (the device would have nowhere to send the stream).
	MediaAdvertiseHost string

	// HLSRoot is the directory where ffmpeg writes HLS playlists/segments.
	HLSRoot string

	// FFmpegPath is the ffmpeg binary used to remux H.264 → HLS.
	FFmpegPath string

	// ClipsRoot is the directory (the "bucket") where recorded clip .mp4 files
	// are stored on the server. Should be a persistent volume.
	ClipsRoot string

	// MediaRetentionDays SEEDS the media_retention_days server setting on first run:
	// how many days stored clips and snapshots are kept before the retention reaper
	// deletes them. 0 = keep forever. Thereafter it is edited live in the admin
	// Server Settings; this env value only sets the initial default. Default 30.
	MediaRetentionDays int

	// BackupsRoot is the directory scheduled gateway-DB backups are written to.
	// Back it with a persistent volume. Empty disables backups entirely.
	BackupsRoot string

	// BackupEnabled / BackupTime / BackupRetention SEED the backup_enabled,
	// backup_time (HH:MM UTC daily) and backup_retention (keep N) server settings on
	// first run. Thereafter they are edited live in the admin Server Settings.
	BackupEnabled   bool
	BackupTime      string
	BackupRetention int
}

// VideoEnabled reports whether live media streaming is configured.
func (c Config) VideoEnabled() bool { return c.MediaAdvertiseHost != "" }

// Load reads configuration from the environment, applying sensible defaults.
func Load() Config {
	dbURL := firstNonEmpty(os.Getenv("DATABASE_URL"), os.Getenv("POSTGRES_URL"))
	authMode := strings.ToLower(os.Getenv("DEVICE_AUTH_MODE"))
	if authMode == "" {
		if dbURL != "" {
			authMode = "postgres"
		} else {
			authMode = "allow_all"
		}
	}

	return Config{
		Gateway:                    getenv("GATEWAY", ""),
		ListenHost:                 getenv("LISTEN_HOST", "0.0.0.0"),
		ListenPort:                 getenvInt("LISTEN_PORT", 33000),
		HTTPPort:                   getenvInt("HTTP_PORT", 8080),
		InternalAPIToken:           os.Getenv("INTERNAL_API_TOKEN"),
		DatabaseURL:                dbURL,
		WebhookURL:                 firstNonEmpty(os.Getenv("DEVICE_WEBHOOK_URL"), os.Getenv("WEBHOOK_URL"), os.Getenv("N8N_WEBHOOK_URL")),
		WebhookTimezoneOffsetHours: getenvFloat("WEBHOOK_TIMEZONE_OFFSET", 0),
		WebhookOutboxMax:           getenvInt("DEVICE_WEBHOOK_OUTBOX_MAX", 200000),
		DeviceTZOffsetHours:        getenvFloat("DEVICE_TZ_OFFSET", 0),
		DeviceAuthMode:             authMode,
		DeviceRejectUnknown:        getenvBool("DEVICE_REJECT_UNKNOWN", true),
		MappingRefreshSeconds:      getenvInt("MAPPING_REFRESH_SECONDS", 60),
		MediaPort:                  getenvInt("MEDIA_PORT", 33001),
		MediaAdvertiseHost:         os.Getenv("MEDIA_ADVERTISE_HOST"),
		HLSRoot:                    getenv("HLS_ROOT", "/tmp/hls"),
		FFmpegPath:                 getenv("FFMPEG_PATH", "ffmpeg"),
		ClipsRoot:                  getenv("CLIPS_ROOT", "/var/lib/gateway/clips"),
		MediaRetentionDays:         getenvInt("MEDIA_RETENTION_DAYS", 30),
		BackupsRoot:                getenv("BACKUPS_ROOT", "/var/lib/gateway/backups"),
		BackupEnabled:              getenvBool("BACKUP_ENABLED", true),
		BackupTime:                 getenv("BACKUP_TIME", "02:00"),
		BackupRetention:            getenvInt("BACKUP_RETENTION", 7),
	}
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getenvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
	}
	return def
}

func getenvFloat(key string, def float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			return f
		}
	}
	return def
}

func getenvBool(key string, def bool) bool {
	if v := os.Getenv(key); v != "" {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "true", "yes", "on":
			return true
		case "0", "false", "no", "off":
			return false
		}
	}
	return def
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

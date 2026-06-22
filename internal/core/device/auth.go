package device

import "context"

// RegisterInfo carries the identity a device presents at registration time,
// used by an Authenticator to decide whether to admit the connection.
type RegisterInfo struct {
	Serial   string
	Protocol string // e.g. "howen"
	RemoteIP string
	Meta     map[string]any
}

// AuthResult is the outcome of an authorization check.
type AuthResult struct {
	Known    bool
	Protocol string // protocol recorded for the device (may refine the guess)
}

// Authenticator decides whether a connecting device is allowed. Implementations
// must be safe for concurrent use. The PostgreSQL store implements this; AllowAll
// is the no-database default.
type Authenticator interface {
	Authorize(ctx context.Context, info RegisterInfo) (AuthResult, error)
	// UpdateStatus records a device lifecycle status (online/sleep/offline).
	UpdateStatus(ctx context.Context, serial, status string) error
}

// AllowAll admits every device and ignores status updates. Used when no device
// registry is configured.
type AllowAll struct{}

func (AllowAll) Authorize(_ context.Context, info RegisterInfo) (AuthResult, error) {
	return AuthResult{Known: true, Protocol: info.Protocol}, nil
}

func (AllowAll) UpdateStatus(context.Context, string, string) error { return nil }

package cathexis

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// config.go implements gateway.ConfigController for Cathexis: reading and writing
// the unit's parameter config (general/network/cameras/events). The device
// answers request_config with a "unit_config" message; update_config is applied
// and acknowledged with "config_updated" — but the device typically reboots, so a
// timeout while waiting is treated as acceptance.

// RequestConfig returns the device's full parameter config. The `modules` arg is
// ignored — Cathexis returns the whole config object.
func (s *session) RequestConfig(ctx context.Context, modules []string) (map[string]any, error) {
	if !s.approved {
		return nil, errors.New("device not approved")
	}
	resp, err := s.request(ctx, "request_config", map[string]any{}, "unit_config")
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// UpdateConfig writes the given config sections (general/network/cameras/events).
func (s *session) UpdateConfig(ctx context.Context, sc map[string]any) error {
	if !s.approved {
		return errors.New("device not approved")
	}
	if len(sc) == 0 {
		return errors.New("no config changes provided")
	}
	ch, err := s.registerPending("config_updated")
	if err != nil {
		return err
	}
	defer s.clearPending("config_updated")
	if err := s.conn.WriteFrame(buildCommand("update_config", sc)); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		// The device reboots to apply config and won't answer; treat as accepted.
		return nil
	case resp := <-ch:
		if msg := strings.TrimSpace(toString(resp["error"])); msg != "" {
			return fmt.Errorf("device rejected config: %s", msg)
		}
		return nil
	}
}

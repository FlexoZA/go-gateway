package cathexis

import (
	"context"
	"errors"
	"fmt"

	"github.com/dfm/device-gateway/internal/core/gateway"
)

// video.go implements gateway.VideoController for Cathexis: live HLS streaming,
// recorded-clip requests (the device uploads a finished MP4 to the media port),
// and a recordings (ring-summary) query.
//
// Camera/profile are passed through to the device 0-based as the API uses them
// (camera 0 = road, 1 = cab; profile 0 = high-res, 1 = low-res).

// StartLive tells the device to stream the camera/profile to our media port and
// returns the HLS path. Cathexis sends no control-channel ack for stream start —
// the live frames arrive on the media connection — so we return once the command
// is sent; the player retries the playlist until ffmpeg produces it.
func (s *session) StartLive(ctx context.Context, camera, profile int) (gateway.StreamInfo, error) {
	if s.conn.Deps.Media == nil {
		return gateway.StreamInfo{}, errors.New("video is not enabled on this gateway")
	}
	if !s.approved {
		return gateway.StreamInfo{}, errors.New("device not approved")
	}
	if s.lifecycle == "sleep" {
		return gateway.StreamInfo{}, gateway.ErrDeviceSleeping
	}
	if camera < 0 || profile < 0 {
		return gateway.StreamInfo{}, errors.New("invalid camera/profile")
	}
	host, port, err := s.mediaTarget()
	if err != nil {
		return gateway.StreamInfo{}, err
	}

	ss := liveSessionID(s.serial, camera, profile)
	if _, err := s.conn.Deps.Media.Register(ss, s.serial, camera, profile); err != nil {
		return gateway.StreamInfo{}, err
	}

	body := map[string]any{
		"camera":         camera,
		"profile":        profile,
		"command":        1, // start
		"period":         0, // indefinite
		"audio":          0, // video-only in v1
		"ip":             host,
		"port":           port,
		"client_id":      ss,
		"stream_version": 1,
	}
	if err := s.conn.WriteFrame(buildCommand("stream", body)); err != nil {
		s.conn.Deps.Media.Stop(ss)
		return gateway.StreamInfo{}, err
	}
	return gateway.StreamInfo{
		SessionID: ss,
		HLSPath:   fmt.Sprintf("%s/%d/%d/stream.m3u8", s.serial, camera, profile),
	}, nil
}

// StopLive tells the device to stop streaming and tears down the HLS output.
func (s *session) StopLive(ctx context.Context, camera, profile int) error {
	if camera < 0 || profile < 0 {
		return errors.New("invalid camera/profile")
	}
	ss := liveSessionID(s.serial, camera, profile)
	werr := s.conn.WriteFrame(buildCommand("stream", map[string]any{
		"camera":  camera,
		"profile": profile,
		"command": 2, // stop
	}))
	if s.conn.Deps.Media != nil {
		s.conn.Deps.Media.Stop(ss)
	}
	return werr
}

// RequestClip asks the device to upload a recorded clip for the camera/time
// window. The device dials the media port and uploads a finished MP4 (type-5
// frames), which is stored directly; this returns once the request is sent (the
// file arrives async — poll the clips API for status).
func (s *session) RequestClip(ctx context.Context, req gateway.ClipRequest) (gateway.ClipInfo, error) {
	if s.conn.Deps.Clips == nil {
		return gateway.ClipInfo{}, errors.New("video is not enabled on this gateway")
	}
	if !s.approved {
		return gateway.ClipInfo{}, errors.New("device not approved")
	}
	if s.lifecycle == "sleep" {
		return gateway.ClipInfo{}, gateway.ErrDeviceSleeping
	}
	if req.Camera < 0 || req.Profile < 0 {
		return gateway.ClipInfo{}, errors.New("invalid camera/profile")
	}
	if req.StartUTC <= 0 || req.EndUTC <= req.StartUTC {
		return gateway.ClipInfo{}, errors.New("invalid start_utc / end_utc")
	}
	host, port, err := s.mediaTarget()
	if err != nil {
		return gateway.ClipInfo{}, err
	}

	// Best-effort preflight: if the device reports a ring summary with no footage
	// overlapping the window, fail fast with a helpful error. If the query itself
	// fails (unsupported/timeout), proceed and let the device decide.
	if recs, qerr := s.QueryRecordings(ctx, req.Camera, req.Profile, req.StartUTC, req.EndUTC); qerr == nil && len(recs) == 0 {
		return gateway.ClipInfo{}, errors.New("no recorded footage for the requested window")
	}

	clipID, ss, err := s.conn.Deps.Clips.NewClip(ctx, s.serial, req.Camera, req.Profile, req.StartUTC, req.EndUTC)
	if err != nil {
		return gateway.ClipInfo{}, err
	}
	body := map[string]any{
		"camera":    req.Camera,
		"profile":   req.Profile,
		"ip":        host,
		"port":      port,
		"start_utc": req.StartUTC,
		"end_utc":   req.EndUTC,
		"client_id": ss,
		"resume":    0,
	}
	if err := s.conn.WriteFrame(buildCommand("request_clip", body)); err != nil {
		s.conn.Deps.Clips.Abort(ss, "send clip request failed")
		return gateway.ClipInfo{}, err
	}
	return gateway.ClipInfo{ClipID: clipID, SessionID: ss, Status: "processing"}, nil
}

// QueryRecordings asks the device for its ring summary and returns the regions
// that overlap the window for the requested profile.
func (s *session) QueryRecordings(ctx context.Context, camera, profile int, startUTC, endUTC int64) ([]gateway.Recording, error) {
	if !s.approved {
		return nil, errors.New("device not approved")
	}
	if s.lifecycle == "sleep" {
		return nil, gateway.ErrDeviceSleeping
	}
	resp, err := s.request(ctx, "request_ring_summary", map[string]any{"camera": camera}, "ring_summary")
	if err != nil {
		return nil, err
	}
	ring, _ := resp["ring"].(map[string]any)
	if ring == nil {
		return nil, nil
	}
	profiles, _ := ring["profiles"].([]any)
	out := []gateway.Recording{}
	for _, pAny := range profiles {
		pm, ok := pAny.(map[string]any)
		if !ok {
			continue
		}
		pf, ok := toFloat(pm["profile"])
		if !ok || int(pf) != profile {
			continue
		}
		regions, _ := pm["regions"].([]any)
		for _, rAny := range regions {
			rm, ok := rAny.(map[string]any)
			if !ok {
				continue
			}
			st, ok1 := toFloat(rm["start_utc"])
			et, ok2 := toFloat(rm["end_utc"])
			if !ok1 || !ok2 {
				continue
			}
			rs, re := int64(st), int64(et)
			// keep only regions overlapping [startUTC, endUTC]
			if rs < endUTC && re > startUTC {
				out = append(out, gateway.Recording{
					Camera:   camera,
					Profile:  profile,
					StartUTC: rs,
					EndUTC:   re,
				})
			}
		}
	}
	return out, nil
}

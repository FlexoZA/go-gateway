package cathexis

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dfm/device-gateway/internal/core/gateway"
)

// review.go implements gateway.ReviewController: "stream review" (API §12), i.e.
// live playback of recorded footage. It reuses the live-HLS pipeline — the device
// streams recorded video (type-2 frames) to the media port, muxed to HLS — but the
// control handshake differs: request_review opens a dedicated review channel
// (identified by client_id), then stream_review_command drives transport
// (PlayFrom/Pause/Resume).

// reviewPlayDelay staggers the initial PlayFrom after request_review so the device
// has a moment to dial back and open the review media channel before we tell it
// where to start. Re-seeking later (ReviewControl) has no such race.
const reviewPlayDelay = 1500 * time.Millisecond

// Review transport commands (stream_review_command `command` field).
const (
	reviewPlayFrom = 1
	reviewPause    = 2
	reviewResume   = 3
)

// StartReview opens a review channel and begins playback from startUTC. It returns
// the HLS path immediately; recorded frames arrive on the review media connection
// (PlayFrom can take several seconds to spool from the SD card), so the player
// retries the playlist until ffmpeg produces it — exactly like live.
func (s *session) StartReview(ctx context.Context, camera, profile int, startUTC int64) (gateway.StreamInfo, error) {
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

	ss := reviewSessionID(s.serial, camera, profile)
	if _, err := s.conn.Deps.Media.RegisterReview(ss, s.serial, camera, profile); err != nil {
		return gateway.StreamInfo{}, err
	}

	// 1) Ask the device to open a review channel back to our media port. It echoes
	//    client_id in that connection's welcome so the media server routes its
	//    frames to this session.
	body := map[string]any{
		"camera":         camera,
		"profile":        profile,
		"ip":             host,
		"port":           port,
		"client_id":      ss,
		"stream_version": 1,
	}
	if err := s.conn.WriteFrame(buildCommand("request_review", body)); err != nil {
		s.conn.Deps.Media.Stop(ss)
		return gateway.StreamInfo{}, err
	}

	// 2) Once the channel is up, tell it where to start. We can't observe the
	//    connect from here, so send the initial PlayFrom after a short delay.
	if startUTC > 0 {
		go func() {
			select {
			case <-s.stop:
				return
			case <-time.After(reviewPlayDelay):
			}
			_ = s.conn.WriteFrame(buildCommand("stream_review_command", map[string]any{"command": reviewPlayFrom, "utc": startUTC}))
		}()
	}

	return gateway.StreamInfo{
		SessionID: ss,
		HLSPath:   fmt.Sprintf("%s/review/%d/%d/stream.m3u8", s.serial, camera, profile),
	}, nil
}

// ReviewControl drives review transport: command 1=PlayFrom(utc) (seek), 2=Pause,
// 3=Resume. camera/profile identify which review (the device tracks one review
// session, but they keep the API symmetric with start/stop).
func (s *session) ReviewControl(ctx context.Context, camera, profile, command int, utc int64) error {
	if !s.approved {
		return errors.New("device not approved")
	}
	if command != reviewPlayFrom && command != reviewPause && command != reviewResume {
		return errors.New("invalid review command")
	}
	payload := map[string]any{"command": command}
	if command == reviewPlayFrom {
		payload["utc"] = utc
	}
	return s.conn.WriteFrame(buildCommand("stream_review_command", payload))
}

// StopReview tears down the review HLS. There is no documented review-stop command,
// so we pause playback and drop the stream; the device's next frame write to the
// now-closed stream fails and it drops the review channel.
func (s *session) StopReview(ctx context.Context, camera, profile int) error {
	ss := reviewSessionID(s.serial, camera, profile)
	if s.conn.Deps.Media != nil {
		s.conn.Deps.Media.Stop(ss)
	}
	if camera < 0 || profile < 0 {
		return errors.New("invalid camera/profile")
	}
	return s.conn.WriteFrame(buildCommand("stream_review_command", map[string]any{"command": reviewPause}))
}

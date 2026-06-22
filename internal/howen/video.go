package howen

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/dfm/device-gateway/internal/core/gateway"
)

// video.go implements gateway.VideoController for a Howen session: starting and
// stopping a live preview (0x4010). The device, once told to start, dials the
// media port and streams frames tagged with the session id we choose here.

// liveSessionID is deterministic per (serial, camera, profile) so a restart
// reuses the same HLS path and the media connection's id always matches.
func liveSessionID(serial string, channel, streamType int) string {
	return fmt.Sprintf("live_%s_%d_%d", serial, channel, streamType)
}

// channelStream maps the API's 0-based camera + profile to Howen's 1-based
// channel and stream type (profile 0 = high → main stream 1; 1 = low → sub 0).
func channelStream(camera, profile int) (channel, streamType int) {
	channel = camera + 1
	streamType = 0
	if profile == 0 {
		streamType = 1
	}
	return channel, streamType
}

// StartLive tells the device to start streaming the camera/profile and returns
// the HLS path once the device acknowledges (0x1010 err=0).
func (s *session) StartLive(ctx context.Context, camera, profile int) (gateway.StreamInfo, error) {
	if s.conn.Deps.Media == nil || s.conn.Deps.MediaAdvertiseHost == "" {
		return gateway.StreamInfo{}, errors.New("video is not enabled on this gateway")
	}
	if s.gate != gateApproved {
		return gateway.StreamInfo{}, errors.New("device not approved")
	}
	if camera < 0 || profile < 0 || profile > 1 {
		return gateway.StreamInfo{}, errors.New("invalid camera/profile")
	}

	channel, streamType := channelStream(camera, profile)
	ss := liveSessionID(s.serial, channel, streamType)

	// Prepare the HLS output before the device starts dialing the media port.
	if _, err := s.conn.Deps.Media.Register(ss, s.serial, camera, profile); err != nil {
		return gateway.StreamInfo{}, err
	}

	ch := make(chan map[string]any, 1)
	s.pendingMu.Lock()
	s.pending[ss] = ch
	s.pendingMu.Unlock()
	defer func() {
		s.pendingMu.Lock()
		delete(s.pending, ss)
		s.pendingMu.Unlock()
	}()

	body := map[string]any{
		"ss":  ss,
		"ch":  strconv.Itoa(channel),
		"si":  strconv.Itoa(streamType),
		"on":  "1",
		"fl":  "1;2", // video only
		"srv": s.conn.Deps.MediaAdvertiseHost,
	}
	if err := s.conn.WriteFrame(buildHowenJSONFrame(msgLivePreview, body)); err != nil {
		s.conn.Deps.Media.Stop(ss)
		return gateway.StreamInfo{}, err
	}

	select {
	case <-ctx.Done():
		s.conn.Deps.Media.Stop(ss)
		return gateway.StreamInfo{}, gateway.ErrCommandTimeout
	case resp := <-ch:
		if code := strings.TrimSpace(toString(resp["err"])); code != "" && code != "0" {
			s.conn.Deps.Media.Stop(ss)
			return gateway.StreamInfo{}, fmt.Errorf("device rejected stream: err=%s", describeHowenError(code))
		}
	}

	return gateway.StreamInfo{
		SessionID: ss,
		HLSPath:   fmt.Sprintf("%s/%d/%d/stream.m3u8", s.serial, camera, profile),
	}, nil
}

// StopLive tells the device to stop the stream and tears down the HLS output.
func (s *session) StopLive(ctx context.Context, camera, profile int) error {
	if camera < 0 || profile < 0 || profile > 1 {
		return errors.New("invalid camera/profile")
	}
	channel, streamType := channelStream(camera, profile)
	ss := liveSessionID(s.serial, channel, streamType)

	body := map[string]any{
		"ss": ss,
		"ch": strconv.Itoa(channel),
		"si": strconv.Itoa(streamType),
		"on": "0",
		"fl": "1;2",
	}
	werr := s.conn.WriteFrame(buildHowenJSONFrame(msgLivePreview, body))
	if s.conn.Deps.Media != nil {
		s.conn.Deps.Media.Stop(ss)
	}
	return werr
}

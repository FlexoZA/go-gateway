package jt808

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/dfm/device-gateway/internal/core/gateway"
)

// video.go implements gateway.VideoController for a JT808 session: live preview
// (0x9101/0x9102) and recorded-clip playback (0x9201). The device dials our
// separate media port (advertised in the command) and streams JT1078 frames,
// which media.go routes to the live HLS manager or the clip registry.

// liveSessionID is the media-manager id for a live stream, derived identically on
// the control side (StartLive) and the media side (from the JT1078 frame's
// serial/channel via the live route).
func liveSessionID(serial string, camera, profile int) string {
	return fmt.Sprintf("live_%s_%d_%d", serial, camera, profile)
}

// channelStream maps the API's 0-based camera + profile to JT1078's 1-based
// channel and the stream type (profile 1 = sub → 1, else main → 0).
func channelStream(camera, profile int) (channel, streamType int) {
	channel = camera + 1
	if profile == 1 {
		streamType = 1
	}
	return channel, streamType
}

// mediaTarget splits the advertised media host:port into the host and TCP port
// fields the video commands carry.
func (s *session) mediaTarget() (string, int, error) {
	host, portStr, err := net.SplitHostPort(s.conn.Deps.MediaAdvertiseHost)
	if err != nil {
		return "", 0, errors.New("video is not enabled on this gateway")
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return "", 0, errors.New("invalid media advertise port")
	}
	return host, port, nil
}

// awaitCommandAck sends a framed command and waits briefly for the device's
// 0x0001 general response acking this command id. An explicit non-zero result is
// a rejection (error); a missing ack is tolerated — not all firmware 0x0001-acks
// a stream request, and the media connection arriving is the real confirmation.
func (s *session) awaitCommandAck(ctx context.Context, cmdID uint16, frame []byte) error {
	actx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	resp, err := s.request(actx, int(cmdID), frame)
	if err != nil {
		if errors.Is(err, gateway.ErrCommandTimeout) || errors.Is(err, context.DeadlineExceeded) {
			s.log().Debug(map[string]any{"event": "command_no_ack", "cmd": fmt.Sprintf("0x%04x", cmdID)})
			return nil
		}
		return err
	}
	if result, ok := resp["result"].(int); ok && result != 0 {
		return fmt.Errorf("device rejected command: result=%d", result)
	}
	return nil
}

// StartLive sends a real-time A/V request (0x9101) pointing the device at our
// media port, and returns the HLS path once the device acks.
func (s *session) StartLive(ctx context.Context, camera, profile int) (gateway.StreamInfo, error) {
	if s.conn.Deps.Media == nil || s.conn.Deps.MediaAdvertiseHost == "" {
		return gateway.StreamInfo{}, errors.New("video is not enabled on this gateway")
	}
	if !s.approved {
		return gateway.StreamInfo{}, gateway.ErrNotConnected
	}
	if camera < 0 || profile < 0 || profile > 1 {
		return gateway.StreamInfo{}, errors.New("invalid camera/profile")
	}
	host, port, err := s.mediaTarget()
	if err != nil {
		return gateway.StreamInfo{}, err
	}
	channel, streamType := channelStream(camera, profile)
	id := liveSessionID(s.serial, camera, profile)

	// Prepare the HLS output and the live route before the device dials back.
	if _, err := s.conn.Deps.Media.Register(id, s.serial, camera, profile); err != nil {
		return gateway.StreamInfo{}, err
	}
	s.routes.setLive(s.serial, channel, camera, profile)

	body := encodeIP(host)
	body = appendU16(body, uint16(port)) // TCP port
	body = appendU16(body, 0)            // UDP port
	body = append(body, byte(channel), 0x01, byte(streamType))

	if err := s.awaitCommandAck(ctx, msgRealtimeAV, buildFrame(msgRealtimeAV, s.phone, s.nextPlatSerial(), body)); err != nil {
		s.conn.Deps.Media.Stop(id)
		s.routes.clearLive(s.serial, channel)
		return gateway.StreamInfo{}, err
	}
	return gateway.StreamInfo{
		SessionID: id,
		HLSPath:   fmt.Sprintf("%s/%d/%d/stream.m3u8", s.serial, camera, profile),
	}, nil
}

// StopLive tells the device to stop the stream (0x9102) and tears down the HLS
// output.
func (s *session) StopLive(ctx context.Context, camera, profile int) error {
	if camera < 0 || profile < 0 || profile > 1 {
		return errors.New("invalid camera/profile")
	}
	channel, _ := channelStream(camera, profile)
	id := liveSessionID(s.serial, camera, profile)
	werr := s.conn.WriteFrame(buildFrame(msgStopAV, s.phone, s.nextPlatSerial(), []byte{byte(channel), 0x00, 0x00}))
	if s.conn.Deps.Media != nil {
		s.conn.Deps.Media.Stop(id)
	}
	s.routes.clearLive(s.serial, channel)
	return werr
}

// RequestClip asks the device to play back recorded footage (0x9201) for the
// camera/time window; the device streams it to the media port, where it is
// remuxed to an .mp4. Returns once the device acks (the file arrives async).
func (s *session) RequestClip(ctx context.Context, req gateway.ClipRequest) (gateway.ClipInfo, error) {
	if s.conn.Deps.Clips == nil || s.conn.Deps.MediaAdvertiseHost == "" {
		return gateway.ClipInfo{}, errors.New("video is not enabled on this gateway")
	}
	if !s.approved {
		return gateway.ClipInfo{}, gateway.ErrNotConnected
	}
	if req.Camera < 0 || req.Profile < 0 || req.Profile > 1 {
		return gateway.ClipInfo{}, errors.New("invalid camera/profile")
	}
	if req.StartUTC <= 0 || req.EndUTC <= req.StartUTC {
		return gateway.ClipInfo{}, errors.New("invalid start_utc / end_utc")
	}
	host, port, err := s.mediaTarget()
	if err != nil {
		return gateway.ClipInfo{}, err
	}
	channel, streamType := channelStream(req.Camera, req.Profile)
	tz := s.tzOffset()

	clipID, ss, err := s.conn.Deps.Clips.NewClip(ctx, s.serial, req.Camera, req.Profile, req.StartUTC, req.EndUTC)
	if err != nil {
		return gateway.ClipInfo{}, err
	}
	s.routes.setPlayback(s.serial, channel, ss)

	body := encodeIP(host)
	body = appendU16(body, uint16(port)) // TCP port
	body = appendU16(body, 0)            // UDP port
	body = append(body,
		byte(channel),
		0x00,             // data type: audio+video
		byte(streamType), // 0 main, 1 sub
		0x00,             // memory type: all
		0x00,             // playback method: normal
		0xff,             // speed: fastest (download)
	)
	body = append(body, bcdTimeFromUTC(req.StartUTC, tz)...)
	body = append(body, bcdTimeFromUTC(req.EndUTC, tz)...)

	if err := s.awaitCommandAck(ctx, msgPlaybackAV, buildFrame(msgPlaybackAV, s.phone, s.nextPlatSerial(), body)); err != nil {
		s.conn.Deps.Clips.Abort(ss, "device rejected playback request")
		s.routes.clearPlayback(s.serial, channel)
		return gateway.ClipInfo{}, err
	}
	return gateway.ClipInfo{ClipID: clipID, SessionID: ss, Status: "processing"}, nil
}

// stopPlayback ends an in-progress playback (0x9202) — exposed as a command.
func (s *session) stopPlayback(camera int) error {
	channel, _ := channelStream(camera, 0)
	body := []byte{byte(channel), 0x02, 0x00, 0, 0, 0, 0, 0, 0}
	return s.conn.WriteFrame(buildFrame(msgPlaybackControl, s.phone, s.nextPlatSerial(), body))
}

func appendU16(b []byte, v uint16) []byte {
	var x [2]byte
	binary.BigEndian.PutUint16(x[:], v)
	return append(b, x[:]...)
}

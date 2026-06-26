package media

import "sync"

// MaxFetchBytes caps an in-memory file fetch so a misbehaving device can't
// balloon memory. Snapshots are small JPEGs; this is a generous ceiling.
const MaxFetchBytes = 16 * 1024 * 1024

// SnapshotFetch tracks in-flight in-memory file downloads (Howen 0x4090
// file-transfer), keyed by session id. The flow: a control-side caller Begins a
// fetch and sends the file-transfer request; the device dials the media port and
// streams the file as 0x0011 frames, which the media server Writes here; on the
// end-of-file marker (or media-connection close) Finish delivers the assembled
// bytes to the waiter on the returned channel.
//
// It deliberately holds no database — a snapshot is fetched and returned inline.
type SnapshotFetch struct {
	mu   sync.Mutex
	byID map[string]*fetchSession
}

type fetchSession struct {
	mu       sync.Mutex
	buf      []byte
	overflow bool
	done     chan []byte
	closed   bool
}

// NewSnapshotFetch constructs an empty registry.
func NewSnapshotFetch() *SnapshotFetch {
	return &SnapshotFetch{byID: map[string]*fetchSession{}}
}

// Begin registers a fetch and returns a channel that receives the assembled file
// bytes when the transfer finishes (the channel is closed without a value if the
// fetch is aborted).
func (s *SnapshotFetch) Begin(ss string) <-chan []byte {
	fs := &fetchSession{done: make(chan []byte, 1)}
	s.mu.Lock()
	s.byID[ss] = fs
	s.mu.Unlock()
	return fs.done
}

// IsFetch reports whether ss belongs to an in-flight file fetch.
func (s *SnapshotFetch) IsFetch(ss string) bool {
	s.mu.Lock()
	_, ok := s.byID[ss]
	s.mu.Unlock()
	return ok
}

func (s *SnapshotFetch) get(ss string) *fetchSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.byID[ss]
}

// Write appends received file bytes to a fetch's buffer (bounded by MaxFetchBytes).
func (s *SnapshotFetch) Write(ss string, data []byte) {
	fs := s.get(ss)
	if fs == nil {
		return
	}
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if fs.closed || fs.overflow {
		return
	}
	if len(fs.buf)+len(data) > MaxFetchBytes {
		fs.overflow = true
		return
	}
	fs.buf = append(fs.buf, data...)
}

// Finish completes a fetch, delivering the assembled bytes to the waiter. A fetch
// that overflowed delivers nil. Idempotent.
func (s *SnapshotFetch) Finish(ss string) {
	s.mu.Lock()
	fs := s.byID[ss]
	delete(s.byID, ss)
	s.mu.Unlock()
	if fs == nil {
		return
	}
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if fs.closed {
		return
	}
	fs.closed = true
	if fs.overflow {
		close(fs.done)
		return
	}
	out := make([]byte, len(fs.buf))
	copy(out, fs.buf)
	fs.done <- out
	close(fs.done)
}

// Abort cancels a fetch without delivering data (e.g. caller timeout). Idempotent.
func (s *SnapshotFetch) Abort(ss string) {
	s.mu.Lock()
	fs := s.byID[ss]
	delete(s.byID, ss)
	s.mu.Unlock()
	if fs == nil {
		return
	}
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if fs.closed {
		return
	}
	fs.closed = true
	close(fs.done)
}

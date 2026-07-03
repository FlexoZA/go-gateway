package gateway

import (
	"net"
	"sync"
)

// connLimiter bounds how many device connections a listener will hold at once — a
// global cap (protects process memory/file descriptors) and an optional per-IP cap
// (blunts a single host opening a flood of sockets). Both are advisory ceilings: a
// zero cap disables that dimension.
//
// The per-IP cap defaults OFF because IoT/GPS fleets commonly sit behind carrier-
// grade NAT, so many legitimate devices share one public IP; enable it only when
// devices have distinct addresses.
type connLimiter struct {
	maxTotal int
	maxPerIP int

	mu    sync.Mutex
	total int
	perIP map[string]int // populated only when maxPerIP > 0
}

func newConnLimiter(maxTotal, maxPerIP int) *connLimiter {
	l := &connLimiter{maxTotal: maxTotal, maxPerIP: maxPerIP}
	if maxPerIP > 0 {
		l.perIP = map[string]int{}
	}
	return l
}

// acquire reserves a slot for ip, returning false (reserving nothing) when the
// global or per-IP cap is already reached. A caller that gets true must release.
func (l *connLimiter) acquire(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.maxTotal > 0 && l.total >= l.maxTotal {
		return false
	}
	if l.maxPerIP > 0 && l.perIP[ip] >= l.maxPerIP {
		return false
	}
	l.total++
	if l.maxPerIP > 0 {
		l.perIP[ip]++
	}
	return true
}

// release returns a slot acquired for ip.
func (l *connLimiter) release(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.total > 0 {
		l.total--
	}
	if l.maxPerIP > 0 {
		if n := l.perIP[ip]; n <= 1 {
			delete(l.perIP, ip) // keep the map from growing without bound
		} else {
			l.perIP[ip] = n - 1
		}
	}
}

// count returns the current live connection total (for logging/metrics).
func (l *connLimiter) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.total
}

// remoteHost extracts the IP (no port) from a connection's remote address, falling
// back to the raw string if it can't be split.
func remoteHost(c net.Conn) string {
	if c == nil {
		return ""
	}
	if host, _, err := net.SplitHostPort(c.RemoteAddr().String()); err == nil {
		return host
	}
	return c.RemoteAddr().String()
}

package httpapi

import (
	"bufio"
	"context"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// metricsSampler periodically samples host CPU utilization so the /api/metrics
// handler can report it without blocking a request (a CPU% is a delta between two
// /proc/stat reads and needs a time window). Memory and Go runtime stats are cheap
// and read on demand in the handler. Linux-only: on other platforms the /proc
// reads fail and the corresponding fields are simply omitted from the response.
type metricsSampler struct {
	startedAt time.Time

	mu         sync.Mutex
	cpuPercent float64 // 0..100 host CPU utilization over the last sample window
	prevTotal  uint64
	prevIdle   uint64
	haveSample bool // false until two samples have been taken
}

func newMetricsSampler() *metricsSampler {
	return &metricsSampler{startedAt: time.Now()}
}

// run samples until ctx is cancelled. It primes the counters immediately so the
// first real percentage is available one interval later.
func (m *metricsSampler) run(ctx context.Context) {
	m.sample() // prime prevTotal/prevIdle; no percentage yet
	t := time.NewTicker(3 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.sample()
		}
	}
}

func (m *metricsSampler) sample() {
	total, idle, ok := readProcStatCPU()
	if !ok {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.prevTotal != 0 { // have a previous reading to diff against
		dTotal := float64(total - m.prevTotal)
		dIdle := float64(idle - m.prevIdle)
		if dTotal > 0 {
			m.cpuPercent = clampPct((dTotal - dIdle) / dTotal * 100)
			m.haveSample = true
		}
	}
	m.prevTotal, m.prevIdle = total, idle
}

// cpu returns the last computed host CPU utilization and whether a real sample
// has been taken yet.
func (m *metricsSampler) cpu() (float64, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cpuPercent, m.haveSample
}

// GET /api/metrics — host CPU/memory utilization plus this process's runtime
// stats, for the dashboard's resource cards. CPU% comes from the background
// sampler; memory and Go runtime numbers are read on demand. Fields that can't be
// read on the host platform are omitted rather than reported as zero.
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	body := map[string]any{
		"num_cpu":       runtime.NumCPU(),
		"goroutines":    runtime.NumGoroutine(),
		"heap_alloc_mb": round1(float64(ms.Alloc) / (1 << 20)),
	}
	if s.telemetryStats != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		body["telemetry"] = s.telemetryStats(ctx)
		cancel()
	}
	if s.metrics != nil {
		if pct, ok := s.metrics.cpu(); ok {
			body["cpu_percent"] = round1(pct)
		}
		body["uptime_seconds"] = int64(time.Since(s.metrics.startedAt).Seconds())
	}
	if used, total, pct, ok := readMemInfo(); ok {
		body["mem_used_mb"] = round1(used)
		body["mem_total_mb"] = round1(total)
		body["mem_percent"] = round1(pct)
	}
	if rss, ok := readProcessRSSMB(); ok {
		body["process_rss_mb"] = round1(rss)
	}
	writeJSON(w, http.StatusOK, body)
}

// readProcStatCPU returns the aggregate CPU jiffy total and idle jiffies from the
// summary "cpu" line of /proc/stat. idle folds in iowait so a stalled disk doesn't
// read as busy CPU.
func readProcStatCPU() (total, idle uint64, ok bool) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return 0, 0, false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}
		// Fields after "cpu": user nice system idle iowait irq softirq steal ...
		fields := strings.Fields(line)[1:]
		for i, fld := range fields {
			v, err := strconv.ParseUint(fld, 10, 64)
			if err != nil {
				continue
			}
			total += v
			if i == 3 || i == 4 { // idle + iowait
				idle += v
			}
		}
		return total, idle, total > 0
	}
	return 0, 0, false
}

// readMemInfo returns host memory used/total (MiB) and used percentage from
// /proc/meminfo, using MemAvailable (the kernel's estimate of reclaimable memory)
// so caches/buffers aren't counted as "used".
func readMemInfo() (usedMB, totalMB, pct float64, ok bool) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0, 0, false
	}
	defer f.Close()
	var totalKB, availKB uint64
	var haveTotal, haveAvail bool
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		switch fields[0] {
		case "MemTotal:":
			totalKB, _ = strconv.ParseUint(fields[1], 10, 64)
			haveTotal = true
		case "MemAvailable:":
			availKB, _ = strconv.ParseUint(fields[1], 10, 64)
			haveAvail = true
		}
	}
	if !haveTotal || !haveAvail || totalKB == 0 || availKB > totalKB {
		return 0, 0, 0, false
	}
	usedKB := totalKB - availKB
	totalMB = float64(totalKB) / 1024
	usedMB = float64(usedKB) / 1024
	pct = clampPct(float64(usedKB) / float64(totalKB) * 100)
	return usedMB, totalMB, pct, true
}

// readProcessRSSMB returns this process's resident set size in MiB from
// /proc/self/statm (field 2 is resident pages).
func readProcessRSSMB() (float64, bool) {
	b, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return 0, false
	}
	fields := strings.Fields(string(b))
	if len(fields) < 2 {
		return 0, false
	}
	pages, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return 0, false
	}
	return float64(pages*uint64(os.Getpagesize())) / (1 << 20), true
}

func clampPct(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

// round1 rounds to one decimal place for tidy JSON.
func round1(v float64) float64 {
	return float64(int64(v*10+0.5)) / 10
}

package fwd

import (
	"fmt"
	"io"
	"sync"
	"time"
)

// ProgressFunc is how a long transfer tells its host how far it is: the stage ("export", "import"), the bytes done, the total bytes (0 when unknown) and the time since it started. A host sets
// Session.Progress (fwdctl prints a line on stderr); a skill that moves a large file reports through a Meter. It reports; it never changes a result.
type ProgressFunc func(stage string, done, total int64, elapsed time.Duration)

// ProgressInterval is the least time between two progress reports of one meter (the last report at the end is always sent).
var ProgressInterval = 5 * time.Second

// Meter counts bytes through a reader or writer and reports them to its session's Progress hook.
type Meter struct {
	hook    ProgressFunc
	stage   string
	total   int64
	started time.Time
	mu      sync.Mutex
	done    int64
	last    time.Time
	sent    int64 // the done value last reported, so Done does not repeat it
}

// NewMeter starts a meter for a stage; total is the byte total when known, else 0. With no hook set on the session it does nothing.
func (s *Session) NewMeter(stage string, total int64) *Meter {
	m := &Meter{stage: stage, total: total, started: time.Now(), sent: -1}
	if s != nil {
		m.hook = s.Progress
	}
	return m
}

// Add counts n bytes and reports if the interval has passed.
func (m *Meter) Add(n int) {
	if m == nil || m.hook == nil || n <= 0 {
		return
	}
	m.mu.Lock()
	m.done += int64(n)
	now := time.Now()
	report := now.Sub(m.last) >= ProgressInterval
	done := m.done
	if report {
		m.last, m.sent = now, done
	}
	m.mu.Unlock()
	if report {
		m.hook(m.stage, done, m.total, now.Sub(m.started))
	}
}

// Done sends the final report (unless the last report already says the same).
func (m *Meter) Done() {
	if m == nil || m.hook == nil {
		return
	}
	m.mu.Lock()
	done, sent := m.done, m.sent
	m.mu.Unlock()
	if done != sent {
		m.hook(m.stage, done, m.total, time.Since(m.started))
	}
}

type meterWriter struct {
	w io.Writer
	m *Meter
}

func (mw meterWriter) Write(p []byte) (int, error) {
	n, err := mw.w.Write(p)
	mw.m.Add(n)
	return n, err
}

type meterReader struct {
	r io.Reader
	m *Meter
}

func (mr meterReader) Read(p []byte) (int, error) {
	n, err := mr.r.Read(p)
	mr.m.Add(n)
	return n, err
}

// Writer passes writes through to w and counts them.
func (m *Meter) Writer(w io.Writer) io.Writer { return meterWriter{w, m} }

// Reader passes reads through from r and counts them.
func (m *Meter) Reader(r io.Reader) io.Reader { return meterReader{r, m} }

// ProgressLine is the one line a host prints for a progress report.
func ProgressLine(stage string, done, total int64, elapsed time.Duration) string {
	mb := func(b int64) float64 { return float64(b) / (1024 * 1024) }
	rate := ""
	if secs := elapsed.Seconds(); secs >= 1 {
		rate = fmt.Sprintf(" (%.1f MB/s)", mb(done)/secs)
	}
	e := int(elapsed.Seconds())
	clock := fmt.Sprintf("%d:%02d", e/60, e%60)
	if total > 0 {
		pct := int(float64(done) / float64(total) * 100)
		return fmt.Sprintf("%s: %.1f of %.1f MB (%d%%)%s, %s elapsed", stage, mb(done), mb(total), pct, rate, clock)
	}
	return fmt.Sprintf("%s: %.1f MB written%s, %s elapsed", stage, mb(done), rate, clock)
}

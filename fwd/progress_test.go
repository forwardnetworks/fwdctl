package fwd

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"
)

type seenProgress struct {
	stage       string
	done, total int64
}

// A meter counts the bytes that pass through a reader or writer and reports them to the session's hook: at most once per interval, and always once more at the end. With no hook it costs nothing.
func TestMeterReportsAtMostOncePerIntervalAndOnceAtTheEnd(t *testing.T) {
	var got []seenProgress
	s := &Session{Progress: func(stage string, done, total int64, _ time.Duration) {
		got = append(got, seenProgress{stage, done, total})
	}}

	old := ProgressInterval
	defer func() { ProgressInterval = old }()
	ProgressInterval = time.Hour
	m := s.NewMeter("import", 100)
	r := m.Reader(strings.NewReader(strings.Repeat("x", 100)))
	if _, err := io.Copy(io.Discard, r); err != nil {
		t.Fatal(err)
	}
	m.Done()
	if len(got) != 1 || got[0] != (seenProgress{"import", 100, 100}) {
		t.Errorf("a long interval gives only the final report: %v", got)
	}

	got = nil
	ProgressInterval = 0
	m = s.NewMeter("export", 0)
	var sink bytes.Buffer
	w := m.Writer(&sink)
	w.Write([]byte("abc"))
	w.Write([]byte("de"))
	m.Done()
	if sink.String() != "abcde" {
		t.Errorf("the writer must pass the bytes through: %q", sink.String())
	}
	if len(got) < 2 || got[len(got)-1] != (seenProgress{"export", 5, 0}) {
		t.Errorf("each write reports with a zero interval: %v", got)
	}

	var none *Session = &Session{}
	m = none.NewMeter("x", 1)
	m.Writer(io.Discard).Write([]byte("a"))
	m.Done() // no hook: no panic, nothing reported
}

func TestProgressLineSaysWhatIsKnown(t *testing.T) {
	if l := ProgressLine("export", 12_900_000, 0, 5*time.Second); !strings.Contains(l, "export") || !strings.Contains(l, "12.3 MB written") || !strings.Contains(l, "MB/s") || !strings.Contains(l, "0:05") {
		t.Errorf("export line: %q", l)
	}
	l := ProgressLine("import", 120*1024*1024, 400*1024*1024, 15*time.Second)
	if !strings.Contains(l, "120.0 of 400.0 MB") || !strings.Contains(l, "30%") || !strings.Contains(l, "0:15") {
		t.Errorf("import line: %q", l)
	}
	if l := ProgressLine("import", 5, 0, 0); strings.Contains(l, "%") {
		t.Errorf("an unknown total has no percent: %q", l)
	}
}

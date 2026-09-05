package supervisor

import (
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	// RingLines is how much of a daemon's past the exec backend keeps per unit;
	// ringLineMax bounds one line, since a daemon in a loop can print anything.
	RingLines   = 500
	ringLineMax = 2048
	// stampLayout is journalctl's short-iso shape, so both backends read alike.
	stampLayout = "2006-01-02T15:04:05-0700"
)

// LineRing keeps a daemon's last lines, stamped as they arrive, across restarts
// of one unit: the journal's stand-in wherever the panel runs the child itself.
type LineRing struct {
	mu      sync.Mutex
	lines   []string
	partial string
	limit   int
	now     func() time.Time
}

func NewLineRing(limit int) *LineRing { return &LineRing{limit: limit, now: time.Now} }

func (r *LineRing) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rest := r.partial + string(p)
	for {
		i := strings.IndexByte(rest, '\n')
		if i < 0 {
			break
		}
		r.pushLocked(rest[:i])
		rest = rest[i+1:]
	}
	r.partial = rest
	return len(p), nil
}

// Flush stamps a final line the daemon never terminated — its last words on
// the way down, more often than not.
func (r *LineRing) Flush() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.partial != "" {
		r.pushLocked(r.partial)
		r.partial = ""
	}
}

func (r *LineRing) pushLocked(line string) {
	line = strings.TrimRight(line, "\r")
	if line == "" {
		return
	}
	if len(line) > ringLineMax {
		line = line[:ringLineMax] + "…"
	}
	r.lines = append(r.lines, r.now().Format(stampLayout)+" "+line)
	if len(r.lines) > r.limit {
		r.lines = slices.Clone(r.lines[len(r.lines)-r.limit:])
	}
}

// Tail returns the last n lines, or all of them for n <= 0.
func (r *LineRing) Tail(n int) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if n <= 0 || n > len(r.lines) {
		n = len(r.lines)
	}
	return slices.Clone(r.lines[len(r.lines)-n:])
}

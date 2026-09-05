package supervisor

import (
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	// ringLines is how much of a daemon's past the exec backend keeps per unit;
	// ringLineMax bounds one line, since a daemon in a loop can print anything.
	ringLines   = 500
	ringLineMax = 2048
	// stampLayout is journalctl's short-iso shape, so both backends read alike.
	stampLayout = "2006-01-02T15:04:05-0700"
)

// lineRing keeps a daemon's last lines, stamped as they arrive, across the
// restarts of one unit: the exec backend's stand-in for the journal.
type lineRing struct {
	mu      sync.Mutex
	lines   []string
	partial string
	limit   int
	now     func() time.Time
}

func newLineRing(limit int) *lineRing { return &lineRing{limit: limit, now: time.Now} }

func (r *lineRing) Write(p []byte) (int, error) {
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
func (r *lineRing) Flush() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.partial != "" {
		r.pushLocked(r.partial)
		r.partial = ""
	}
}

func (r *lineRing) pushLocked(line string) {
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

// tail returns the last n lines, or all of them for n <= 0.
func (r *lineRing) tail(n int) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if n <= 0 || n > len(r.lines) {
		n = len(r.lines)
	}
	return slices.Clone(r.lines[len(r.lines)-n:])
}

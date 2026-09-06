package service

import (
	"sort"
	"sync"
)

/*
sightingsRegistry is which clients other nodes have reported live, and when:
the key the master's pass asks the attribution rows with, so a client served by
nodes alone is decided here without every row of every node being read each
pass. Filled by every merge the sync makes; a restart forgets it for one tick.
*/
type sightingsRegistry struct {
	mu   sync.Mutex
	seen map[string]map[string]int64
}

var nodeSightings = &sightingsRegistry{seen: map[string]map[string]int64{}}

func (r *sightingsRegistry) Note(guid, email string, lastSeenUnix int64) {
	if guid == "" || email == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	byGuid := r.seen[email]
	if byGuid == nil {
		byGuid = map[string]int64{}
		r.seen[email] = byGuid
	}
	if lastSeenUnix > byGuid[guid] {
		byGuid[guid] = lastSeenUnix
	}
}

// Fresh is every client some node reported at or after since, sorted. Older
// sightings are forgotten on the way, so the registry stays the size of the
// cluster's online set.
func (r *sightingsRegistry) Fresh(sinceUnix int64) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.seen))
	for email, byGuid := range r.seen {
		for guid, seen := range byGuid {
			if seen < sinceUnix {
				delete(byGuid, guid)
			}
		}
		if len(byGuid) == 0 {
			delete(r.seen, email)
			continue
		}
		out = append(out, email)
	}
	sort.Strings(out)
	return out
}

func (r *sightingsRegistry) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = map[string]map[string]int64{}
}

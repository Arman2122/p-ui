/*
Package presence answers "who is connected right now", for the whole panel.

It used to live on xray.Process, which made the answer depend on a core the
panel does not require: RefreshLocalOnlineClients was a silent no-op when Xray
had never started, and every reader returned an empty set on a nil process. A
panel serving only MTProto or kernel WireGuard therefore reported every client
offline, forever, while CoreSupervise was watching those cores come up. F089.

Nothing here knows about a core. It is a grace-windowed set of last-seen stamps
and the node subtrees a master has been told about, guarded by one lock.
*/
package presence

import "sync"

// Set is the panel's presence view: what this host saw, plus what its nodes
// reported. The zero value is usable.
type Set struct {
	mu sync.RWMutex

	/*
		local is the last time THIS panel saw traffic for each email, and localTags
		the same for each inbound tag.

		Kept apart from the shared client_traffics.last_online column on purpose:
		that column is bumped by remote-node syncs too, and reading presence from
		it would report a client that is only connected to a node as online here.

		Both are needed. A core's per-user stat aggregates across every inbound a
		client is attached to, so an online email alone cannot say WHICH inbound
		carried it; pairing the two lets a per-inbound view drop a multi-inbound
		client from the inbounds that saw nothing.
	*/
	local     map[string]int64
	localTags map[string]int64

	// Derived from the two maps on every refresh, so a reader never walks them.
	online     []string
	activeTags []string

	/*
		nodeTrees holds, per direct remote node (keyed by that node's panel-local
		id), the GUID-keyed subtree it reported — its own clients under its
		panelGuid plus every descendant under theirs.

		Keying the stored value by GUID rather than node id is what lets a master
		attribute a client three hops down to the node that physically hosts it;
		the outer node-id key exists only so a failed probe can drop that whole
		branch in one go.
	*/
	nodeTrees map[int]map[string][]string
}

// shared is the panel's one presence view. A package-level value because the
// readers are spread across the service and job layers and none of them owns it.
var shared = &Set{}

// Shared returns the panel's presence view.
func Shared() *Set { return shared }

/*
RefreshLocal records that each email and each inbound tag had traffic at now,
then rebuilds the derived sets from everything seen within graceMs and prunes
what is older.

Pass nil to prune only: the node sync does that, so a core that has stopped
reporting still ages its clients out between local polls.
*/
func (s *Set) RefreshLocal(activeEmails, activeInboundTags []string, now, graceMs int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.online = refreshWindow(&s.local, activeEmails, now, graceMs)
	s.activeTags = refreshWindow(&s.localTags, activeInboundTags, now, graceMs)
}

// refreshWindow stamps seen at now and returns everything still inside the
// window, deleting the rest. The window is half-open: idle for exactly graceMs
// ages out, idle for graceMs-1 does not.
func refreshWindow(stamps *map[string]int64, seen []string, now, graceMs int64) []string {
	if *stamps == nil {
		*stamps = make(map[string]int64, len(seen))
	}
	for _, key := range seen {
		(*stamps)[key] = now
	}
	out := make([]string, 0, len(*stamps))
	for key, at := range *stamps {
		if now-at < graceMs {
			out = append(out, key)
		} else {
			delete(*stamps, key)
		}
	}
	return out
}

/*
Online is every client connected anywhere the panel can see: this host's own,
plus every node's. Deduped, so a client on both a local and a node inbound
surfaces once.
*/
func (s *Set) Online() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.nodeTrees) == 0 {
		// The single-panel path, which is most of them: no map, no dedupe.
		return s.online
	}
	seen := make(map[string]struct{}, len(s.online))
	out := make([]string, 0, len(s.online))
	add := func(emails []string) {
		for _, email := range emails {
			if _, dup := seen[email]; dup {
				continue
			}
			seen[email] = struct{}{}
			out = append(out, email)
		}
	}
	add(s.online)
	for _, tree := range s.nodeTrees {
		for _, emails := range tree {
			add(emails)
		}
	}
	return out
}

// LocalOnline is the emails this host itself saw within the window. The service
// layer keys these under the panel's own GUID.
func (s *Set) LocalOnline() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return copyOf(s.online)
}

// LocalActiveTags is the inbound tags this host carried traffic through within
// the window. Only a local core reports per-inbound activity; a node missing
// from that map means "do not gate", not "nothing was active".
func (s *Set) LocalActiveTags() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return copyOf(s.activeTags)
}

func copyOf(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

/*
MergedNodeTrees is the union of every direct node's subtree, keyed by the GUID
of the node that physically hosts each set.

Each child already reports its own descendants under their GUIDs, so merging the
direct children yields the whole tree at any depth. A set reported under the same
GUID by more than one path is deduped; empty sets are omitted.
*/
func (s *Set) MergedNodeTrees() map[string][]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.nodeTrees) == 0 {
		return map[string][]string{}
	}
	out := make(map[string][]string)
	seen := make(map[string]map[string]struct{})
	for _, tree := range s.nodeTrees {
		for guid, emails := range tree {
			if guid == "" || len(emails) == 0 {
				continue
			}
			dedup := seen[guid]
			if dedup == nil {
				dedup = make(map[string]struct{}, len(emails))
				seen[guid] = dedup
			}
			for _, email := range emails {
				if _, ok := dedup[email]; ok {
					continue
				}
				dedup[email] = struct{}{}
				out[guid] = append(out[guid], email)
			}
		}
	}
	return out
}

// SetNodeTree replaces one direct node's whole subtree. The sync always sends
// the full subtree per tick, so replacing is the correct merge.
func (s *Set) SetNodeTree(nodeID int, tree map[string][]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.nodeTrees == nil {
		s.nodeTrees = map[int]map[string][]string{}
	}
	s.nodeTrees[nodeID] = tree
}

// ClearNodeTree drops a node's whole contribution, so a downed node — and
// everything behind it — stops being reported as online until it answers again.
func (s *Set) ClearNodeTree(nodeID int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.nodeTrees, nodeID)
}

func (s *Set) RetainNodeTrees(keep map[int]bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for nodeID := range s.nodeTrees {
		if !keep[nodeID] {
			delete(s.nodeTrees, nodeID)
		}
	}
}

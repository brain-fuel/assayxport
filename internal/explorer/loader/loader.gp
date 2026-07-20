// Package loader is the explorer's loading algebra: it decides what to fetch
// next (a priority Scheduler) and what to drop when memory runs short (an LRU
// Cache with pins), expressed over goforge.dev/cadence's Strategy vocabulary.
//
// It is pure state with no I/O and no syscall/js, so it unit-tests on every
// GOOS. The browser layer (cmd/axwasm + explorer.html) is the interpreter: it
// asks the Scheduler what to fetch, performs the fetches, reports sizes to the
// Cache, and executes the Cache's eviction list. This realizes the "TEA-style
// client interpreter" cadence's design anticipates -- the Policy is a real
// cadence.Policy, and the priority/eviction machinery is the adaptive behavior
// cadence leaves to its Profile seam.
package loader

import (
	"sort"

	"goforge.dev/assayxport/internal/schema"
	"goforge.dev/cadence"
)

// Intent is how much a region is needed right now. Higher intent loads sooner
// and is evicted later. It is the value we carry in cadence's Profile.
type Intent int

const (
	Distant  Intent = iota // not near the viewport; lowest priority
	Adjacent               // a sibling on the current level (likely-next)
	Hovered                // pointer/focus intent (prefetch)
	Visible                // in view now
	Pinned                 // the open package / current level; never evicted
)

// Profile is the adaptive signal a Policy reads. It satisfies cadence.Profile
// (which is interface{}), so a cadence Policy can switch on it.
type Profile struct {
	Intent   Intent
	Pressure bool // memory is over budget
}

// Policy is the explorer's adaptive cadence.Policy: it maps a region and the
// current Profile to a cadence.Strategy. Under no-JS it returns Eager, honoring
// cadence's fallback law (the static ax assay file renders everything inline).
type Policy struct{}

func (Policy) StrategyFor(_ string, ctx cadence.RequestContext, prof cadence.Profile) cadence.Strategy {
	if ctx.NoJS {
		return cadence.Eager()
	}
	p, _ := prof.(Profile)
	switch p.Intent {
	case Pinned, Visible:
		return cadence.Deferred(cadence.Client(), cadence.OnLoad())
	case Hovered:
		return cadence.Deferred(cadence.Client(), cadence.OnHover())
	default: // Adjacent, Distant
		return cadence.Deferred(cadence.Client(), cadence.OnVisible())
	}
}

// Priority maps an intent to a scheduler priority (higher fetches first). It is
// derived through the Policy so the ordering and the declared strategy stay in
// one place: OnLoad > OnHover > OnVisible.
func Priority(in Intent) int {
	s := Policy{}.StrategyFor("", cadence.RequestContext{}, Profile{Intent: in})
	return cadence.StrategyFold(s, cadence.StrategyCases[int]{
		Eager: func() int { return 100 },
		Deferred: func(_ cadence.Where, on cadence.Trigger) int {
			return cadence.TriggerFold(on, cadence.TriggerCases[int]{
				OnLoad: func() int { return 40 },
				OnHover: func() int { return 30 },
				OnVisible: func() int { return 20 + int(in) },
			})
		},
		Live: func() int { return 100 },
	})
}

// Scheduler is a bounded-concurrency priority queue over ids (shard paths). It
// hands out the highest-priority pending id whenever a slot is free. Navigating
// away is just Reset (drop everything still pending) plus new Wants; in-flight
// fetches finish on their own and report Done.
type Scheduler struct {
	Cap      int
	want     map[schema.PackageID]schema.Priority
	inflight map[schema.PackageID]bool
	state    map[schema.PackageID]LoadState
}

type LoadState enum {
	PendingLoad(priority schema.Priority)
	InFlightLoad
	LoadedLoad(size schema.ByteSize)
	EvictedLoad
}

// FetchPermit is minted only when a pending package moves in flight.
type FetchPermit struct { id schema.PackageID }

func FetchPermitID(p FetchPermit) string { return schema.PackageIDString(p.id) }

func NewScheduler(capacity int) *Scheduler {
	if capacity < 1 {
		capacity = 1
	}
	return &Scheduler{Cap: capacity, want: map[schema.PackageID]schema.Priority{}, inflight: map[schema.PackageID]bool{}, state: map[schema.PackageID]LoadState{}}
}

// Want enqueues id at prio (or raises its priority). An already-inflight id is
// ignored (it is already being fetched).
func (s *Scheduler) Want(id string, prio int) {
	key := schema.MustPackageID(id)
	priority := schema.Priority(prio)
	if s.inflight[key] {
		return
	}
	if p, ok := s.want[key]; !ok || priority > p {
		s.want[key] = priority
		s.state[key] = PendingLoad(priority)
	}
}

// Forget drops a pending id (e.g. it just loaded by another path).
func (s *Scheduler) Forget(id string) { key := schema.MustPackageID(id); delete(s.want, key); s.state[key] = EvictedLoad() }

// Reset clears the pending queue. In-flight fetches are left to finish.
func (s *Scheduler) Reset() { s.want = map[schema.PackageID]schema.Priority{} }

// Inflight reports how many fetches are outstanding.
func (s *Scheduler) Inflight() int { return len(s.inflight) }

// Pending reports how many ids are queued.
func (s *Scheduler) Pending() int { return len(s.want) }

// Next returns up to (Cap - inflight) highest-priority pending ids, moving them
// to in-flight. Ties break by id for determinism.
func (s *Scheduler) Next() []string {
	permits := s.NextPermits()
	if len(permits) == 0 { return nil }
	out := make([]string, len(permits))
	for i, permit := range permits { out[i] = FetchPermitID(permit) }
	return out
}

// NextPermits performs the pending→in-flight transition and returns the
// capability required to complete it.
func (s *Scheduler) NextPermits() []FetchPermit {
	slots := s.Cap - len(s.inflight)
	if slots <= 0 || len(s.want) == 0 {
		return nil
	}
	type kv struct {
		id schema.PackageID
		p  schema.Priority
	}
	all := make([]kv, 0, len(s.want))
	for id, p := range s.want {
		all = append(all, kv{id, p})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].p != all[j].p {
			return all[i].p > all[j].p
		}
		return schema.PackageIDString(all[i].id) < schema.PackageIDString(all[j].id)
	})
	out := make([]FetchPermit, 0, slots)
	for _, e := range all {
		if slots <= 0 {
			break
		}
		delete(s.want, e.id)
		s.inflight[e.id] = true
		s.state[e.id] = InFlightLoad()
		out = append(out, FetchPermit{id: e.id})
		slots--
	}
	return out
}

// Done marks an in-flight fetch complete (success or failure).
func (s *Scheduler) Done(id string) { delete(s.inflight, schema.MustPackageID(id)) }

// Complete consumes a permit exactly once on every Go+ path. Generated Go
// callers receive the runtime use-once cell as an additional boundary guard.
func Complete(s *Scheduler, 1 permit FetchPermit) {
	id := permit.id
	delete(s.inflight, id)
	s.state[id] = LoadedLoad(schema.ByteSize(0))
}

func (s *Scheduler) State(id string) LoadState {
	key := schema.MustPackageID(id)
	if state, ok := s.state[key]; ok { return state }
	return EvictedLoad()
}

// Cache is an LRU of loaded ids with a byte budget and a pin set. Pinned ids
// (the open package, the current level) are never evicted. A Budget <= 0
// disables eviction entirely.
type Cache struct {
	Budget schema.ByteBudget
	size   map[string]schema.ByteSize
	seq    map[string]int64
	pin    map[string]bool
	clock  int64
	total  schema.ByteSize
}

func NewCache(budget int64) *Cache {
	return &Cache{Budget: schema.ByteBudget(budget), size: map[string]schema.ByteSize{}, seq: map[string]int64{}, pin: map[string]bool{}}
}

// Note records that id is loaded and occupies size bytes, marking it most
// recently used.
func (c *Cache) Note(id string, rawSize int64) {
	size := schema.ByteSize(rawSize)
	if _, ok := c.size[id]; !ok {
		c.total += size
	} else {
		c.total += size - c.size[id]
	}
	c.size[id] = size
	c.Touch(id)
}

// Touch marks id most recently used (called on access so hot shards survive).
func (c *Cache) Touch(id string) { c.clock++; c.seq[id] = c.clock }

// Pin protects id from eviction; Unpin releases it; UnpinAll clears all pins.
func (c *Cache) Pin(id string)         { c.pin[id] = true }
func (c *Cache) Unpin(id string)       { delete(c.pin, id) }
func (c *Cache) UnpinAll()             { c.pin = map[string]bool{} }
func (c *Cache) Pinned(id string) bool { return c.pin[id] }

// Loaded reports whether id is in the cache. Total returns the current bytes.
func (c *Cache) Loaded(id string) bool { _, ok := c.size[id]; return ok }
func (c *Cache) Total() int64          { return int64(c.total) }

// Forget removes id from the cache (after it has been evicted).
func (c *Cache) Forget(id string) {
	if s, ok := c.size[id]; ok {
		c.total -= s
		delete(c.size, id)
		delete(c.seq, id)
	}
}

// Overflow returns the ids to evict -- least-recently-used first, skipping
// pinned ids -- until the total would fit the budget. It does not mutate the
// cache; the caller drops each returned id (in the engine and the DOM) and then
// calls Forget. Returns nil when within budget or eviction is disabled.
func (c *Cache) Overflow() []string {
	if c.Budget <= 0 || c.total <= schema.ByteSize(c.Budget) {
		return nil
	}
	type kv struct {
		id  string
		seq int64
	}
	cand := make([]kv, 0, len(c.size))
	for id := range c.size {
		if !c.pin[id] {
			cand = append(cand, kv{id, c.seq[id]})
		}
	}
	sort.Slice(cand, func(i, j int) bool { return cand[i].seq < cand[j].seq })
	out := []string{}
	t := c.total
	for _, e := range cand {
		if t <= schema.ByteSize(c.Budget) {
			break
		}
		out = append(out, e.id)
		t -= c.size[e.id]
	}
	return out
}

package loader

import (
	"testing"

	"goforge.dev/cadence"
)

func TestPolicyFallbackLaw(t *testing.T) {
	// Under no-JS every strategy must be Eager (cadence's fallback law).
	for _, in := range []Intent{Distant, Adjacent, Hovered, Visible, Pinned} {
		s := Policy{}.StrategyFor("r", cadence.RequestContext{NoJS: true}, Profile{Intent: in})
		if s.Kind != cadence.Eager {
			t.Fatalf("no-JS intent %d gave %v, want Eager", in, s.Kind)
		}
	}
}

func TestPolicyMapsIntentToTrigger(t *testing.T) {
	cases := map[Intent]cadence.Trigger{
		Visible: cadence.OnLoad, Pinned: cadence.OnLoad,
		Hovered: cadence.OnHover, Adjacent: cadence.OnVisible, Distant: cadence.OnVisible,
	}
	for in, want := range cases {
		s := Policy{}.StrategyFor("r", cadence.RequestContext{}, Profile{Intent: in})
		if s.Kind != cadence.Deferred || s.Where != cadence.Client || s.On != want {
			t.Fatalf("intent %d gave %+v, want Deferred/Client/%v", in, s, want)
		}
	}
}

func TestPriorityOrder(t *testing.T) {
	if !(Priority(Visible) > Priority(Hovered) && Priority(Hovered) > Priority(Adjacent) && Priority(Adjacent) > Priority(Distant)) {
		t.Fatalf("priority not ordered: vis=%d hov=%d adj=%d dist=%d",
			Priority(Visible), Priority(Hovered), Priority(Adjacent), Priority(Distant))
	}
}

func TestSchedulerRespectsCapAndPriority(t *testing.T) {
	s := NewScheduler(2)
	s.Want("low", Priority(Distant))
	s.Want("high", Priority(Visible))
	s.Want("mid", Priority(Hovered))
	first := s.Next()
	if len(first) != 2 {
		t.Fatalf("cap 2 gave %d: %v", len(first), first)
	}
	if first[0] != "high" || first[1] != "mid" {
		t.Fatalf("priority order wrong: %v", first)
	}
	if got := s.Next(); got != nil {
		t.Fatalf("no free slot but Next returned %v", got)
	}
	s.Done("high")
	third := s.Next()
	if len(third) != 1 || third[0] != "low" {
		t.Fatalf("after a slot freed expected [low], got %v", third)
	}
}

func TestSchedulerDedupAndRaise(t *testing.T) {
	s := NewScheduler(1)
	s.Want("a", 10)
	s.Want("a", 30) // raise
	s.Want("a", 20) // no lower
	if s.Pending() != 1 {
		t.Fatalf("dedup failed: pending=%d", s.Pending())
	}
	// inflight ids are not re-queued
	s.Next()
	s.Want("a", 99)
	if s.Pending() != 0 {
		t.Fatalf("re-queued an inflight id: pending=%d", s.Pending())
	}
}

func TestCacheEvictsLRUUnpinned(t *testing.T) {
	c := NewCache(100)
	c.Note("a", 40)
	c.Note("b", 40)
	c.Note("c", 40) // total 120 > 100
	// a is oldest and unpinned -> evict a (to reach <=100).
	ov := c.Overflow()
	if len(ov) != 1 || ov[0] != "a" {
		t.Fatalf("overflow = %v, want [a]", ov)
	}
}

func TestCachePinNeverEvicts(t *testing.T) {
	c := NewCache(100)
	c.Note("a", 40)
	c.Note("b", 40)
	c.Note("c", 40)
	c.Pin("a") // oldest but pinned
	ov := c.Overflow()
	for _, id := range ov {
		if id == "a" {
			t.Fatalf("evicted pinned id a: %v", ov)
		}
	}
	if len(ov) == 0 {
		t.Fatal("expected some eviction with b/c unpinned over budget")
	}
}

func TestCacheTouchProtectsRecent(t *testing.T) {
	c := NewCache(100)
	c.Note("a", 40)
	c.Note("b", 40)
	c.Touch("a") // a now newest
	c.Note("c", 40)
	ov := c.Overflow()
	if len(ov) != 1 || ov[0] != "b" {
		t.Fatalf("overflow = %v, want [b] (a was touched)", ov)
	}
}

func TestCacheForgetReducesTotal(t *testing.T) {
	c := NewCache(0) // eviction disabled
	c.Note("a", 40)
	c.Note("b", 60)
	if c.Total() != 100 {
		t.Fatalf("total=%d, want 100", c.Total())
	}
	c.Forget("a")
	if c.Total() != 60 || c.Loaded("a") {
		t.Fatalf("after forget a: total=%d loaded=%v", c.Total(), c.Loaded("a"))
	}
	if c.Overflow() != nil {
		t.Fatal("budget 0 must disable eviction")
	}
}

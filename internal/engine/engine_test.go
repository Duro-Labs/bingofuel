package engine

import (
	"context"
	"testing"
	"time"

	"github.com/fernandoduro/bingofuel/internal/model"
	"github.com/fernandoduro/bingofuel/internal/providers"
)

type fake struct {
	calls  *int
	err    *error
	resets *time.Time
}

func (fake) ID() string          { return "fake" }
func (fake) Title() string       { return "Fake" }
func (fake) Color() string       { return "#000000" }
func (fake) Local() bool         { return false }
func (fake) Selection() []string { return nil }
func (f fake) Fetch(context.Context, time.Time) (model.Group, error) {
	*f.calls++
	g := model.Group{ID: "fake", Title: "Fake"}
	if *f.err != nil {
		return g, *f.err
	}
	g.Meters = []model.Meter{{ID: "session", Kind: model.KindWindow, Label: "5-Hour", Percent: 40, ResetsAt: f.resets}}
	return g, nil
}

type rig struct {
	e     *Engine
	calls int
	err   error
	clock time.Time
}

func newRig(t *testing.T, resets *time.Time) *rig {
	r := &rig{clock: time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)}
	r.e = &Engine{CacheDir: t.TempDir(), MaxAge: 5 * time.Minute, Now: func() time.Time { return r.clock }}
	r.e.Providers = []providers.Provider{fake{calls: &r.calls, err: &r.err, resets: resets}}
	return r
}

func (r *rig) run() model.Group { return r.e.Run(context.Background()).Groups[0] }

func TestCacheFloor(t *testing.T) {
	r := newRig(t, nil)
	if g := r.run(); g.Status != model.StatusOK || r.calls != 1 || !g.Meters[0].Show {
		t.Fatalf("first run: %+v calls=%d", g, r.calls)
	}
	r.clock = r.clock.Add(4 * time.Minute)
	r.run()
	if r.calls != 1 {
		t.Errorf("served from cache inside the floor expected; calls=%d", r.calls)
	}
	r.clock = r.clock.Add(2 * time.Minute)
	r.run()
	if r.calls != 2 {
		t.Errorf("refetch after the floor expected; calls=%d", r.calls)
	}
}

func TestRateLimitHold(t *testing.T) {
	r := newRig(t, nil)
	r.run()
	r.err = &providers.Error{Status: model.StatusRateLimited, Message: "429"}
	r.clock = r.clock.Add(6 * time.Minute)
	g := r.run()
	if g.Status != model.StatusRateLimited || len(g.Meters) != 1 || g.Status.Dimmed() {
		t.Fatalf("429 should keep figures as current: %+v", g)
	}
	r.err = nil
	r.clock = r.clock.Add(20 * time.Minute)
	r.run()
	if r.calls != 2 {
		t.Errorf("no fetch during the 30-minute hold; calls=%d", r.calls)
	}
	r.clock = r.clock.Add(11 * time.Minute)
	if g := r.run(); r.calls != 3 || g.Status != model.StatusOK {
		t.Errorf("fetch after the hold: calls=%d status=%s", r.calls, g.Status)
	}
}

func TestErrorKeepsFiguresDimmedAndRetries(t *testing.T) {
	r := newRig(t, nil)
	r.run()
	r.clock = r.clock.Add(6 * time.Minute)
	r.err = &providers.Error{Status: model.StatusStale, Message: "could not reach Fake"}
	g := r.run()
	if g.Status != model.StatusStale || !g.Status.Dimmed() || len(g.Meters) != 1 || g.LastSuccess == nil {
		t.Fatalf("stale group: %+v", g)
	}
	r.clock = r.clock.Add(30 * time.Second)
	r.run()
	if r.calls != 2 {
		t.Errorf("no retry inside 60 s; calls=%d", r.calls)
	}
	r.clock = r.clock.Add(31 * time.Second)
	r.run()
	if r.calls != 3 {
		t.Errorf("retry after 60 s; calls=%d", r.calls)
	}
}

func TestPlanLapsedDropsFigures(t *testing.T) {
	r := newRig(t, nil)
	r.run()
	r.clock = r.clock.Add(6 * time.Minute)
	r.err = &providers.Error{Status: model.StatusPlanLapsed, Message: "no plan"}
	if g := r.run(); len(g.Meters) != 0 {
		t.Errorf("lapsed plan must not carry figures: %+v", g)
	}
}

func TestResetPassedForcesRefetch(t *testing.T) {
	reset := time.Date(2026, 11, 17, 12, 2, 0, 0, time.UTC)
	r := newRig(t, &reset)
	r.run()
	r.clock = r.clock.Add(3 * time.Minute) // inside the floor, but past the reset
	r.run()
	if r.calls != 2 {
		t.Errorf("a passed reset should refetch; calls=%d", r.calls)
	}
}

func TestFirstFailureHasNoFigures(t *testing.T) {
	r := newRig(t, nil)
	r.err = &providers.Error{Status: model.StatusAuthExpired, Message: "run fake"}
	g := r.run()
	if g.Status != model.StatusAuthExpired || len(g.Meters) != 0 || g.Meters == nil {
		t.Errorf("first failure: %+v (meters must be an empty list, not null)", g)
	}
}

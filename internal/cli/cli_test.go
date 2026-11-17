package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/fernandoduro/bingofuel/internal/model"
)

func TestCountdown(t *testing.T) {
	for d, want := range map[time.Duration]string{
		3*time.Hour + 22*time.Minute:  "03:22",
		75*time.Hour + 10*time.Minute: "3d 3h",
		-time.Minute:                  "00:00",
		23*time.Hour + 59*time.Minute: "23:59",
		24 * time.Hour:                "1d 0h",
	} {
		if got := Countdown(d); got != want {
			t.Errorf("Countdown(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestRenderStatus(t *testing.T) {
	now := time.Now()
	out := model.Output{ConfigFound: true, Groups: []model.Group{
		{ID: "openrouter", Title: "OpenRouter", Status: model.StatusOK, Meters: []model.Meter{
			{ID: "credits", Kind: model.KindCredit, Label: "Credits", Show: true, Percent: 0.7, Remaining: model.F(9.93), Total: model.F(10), Currency: "USD"}}},
		{ID: "typesafe", Title: "JEV", Status: model.StatusOK, Meters: []model.Meter{
			{ID: "credits", Kind: model.KindCredit, Label: "Credits", Show: true, Percent: 1, Remaining: model.F(4.98), Total: model.F(5), Currency: "USD", Estimate: true}}},
		{ID: "claude", Title: "Claude", Status: model.StatusAuthExpired, Message: "run claude", LastSuccess: model.T(now.Add(-2 * time.Hour))},
	}}
	var b bytes.Buffer
	renderStatus(&b, out, now, false)
	s := b.String()
	for _, want := range []string{"$9.93 left", "≈$4.98 left", "! run claude (last good data 2.0 h ago)"} {
		if !strings.Contains(s, want) {
			t.Errorf("status output missing %q:\n%s", want, s)
		}
	}
}

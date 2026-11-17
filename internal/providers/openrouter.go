package providers

import (
	"context"
	"fmt"
	"time"

	"github.com/fernandoduro/bingofuel/internal/config"
	"github.com/fernandoduro/bingofuel/internal/model"
	"github.com/fernandoduro/bingofuel/internal/secrets"
)

const openRouterBase = "https://openrouter.ai/api/v1"

type OpenRouter struct {
	Cfg  config.OpenRouter
	Base string // test override
	// Key overrides the secret lookup (setup's live test, tests).
	Key string
}

func (OpenRouter) ID() string          { return "openrouter" }
func (OpenRouter) Title() string       { return "OpenRouter" }
func (OpenRouter) Color() string       { return "#7AB8FF" }
func (OpenRouter) Local() bool         { return false }
func (OpenRouter) Selection() []string { return nil }

func (o OpenRouter) key() (string, error) {
	if o.Key != "" {
		return o.Key, nil
	}
	k, _, err := secrets.Get("openrouter", o.Cfg.KeyEnv)
	if err != nil {
		return "", errf(model.StatusNotConfigured, "no OpenRouter key: run bingofuel setup")
	}
	return k, nil
}

func (o OpenRouter) Fetch(ctx context.Context, now time.Time) (model.Group, error) {
	g := model.Group{ID: o.ID(), Title: o.Title(), Color: o.Color()}
	k, err := o.key()
	if err != nil {
		return g, err
	}
	base := o.Base
	if base == "" {
		base = openRouterBase
	}
	h := map[string]string{"Authorization": "Bearer " + k}

	body, _, err := get(ctx, base+"/credits", h, "OpenRouter")
	switch s, _ := StatusOf(err); {
	case err == nil:
		m, derr := decode(body, "OpenRouter")
		if derr != nil {
			return g, derr
		}
		if meter, ok := parseOpenRouterCredits(m); ok {
			g.Meters = append(g.Meters, meter)
		}
	case s == model.StatusForbidden:
		// Some accounts need a management key for /credits. Drop only the
		// balance; the key details below may still be readable.
		g.Details = append(g.Details, model.Detail{Label: "Balance", Value: "this key cannot read the account balance (403)"})
	case s == model.StatusAuthExpired:
		return g, errf(model.StatusAuthExpired, "OpenRouter rejected the API key: run bingofuel setup")
	default:
		return g, err
	}

	// The key's own spending cap goes in the popup only; failures here are not fatal.
	if body, _, err := get(ctx, base+"/key", h, "OpenRouter"); err == nil {
		if m, err := decode(body, "OpenRouter"); err == nil {
			g.Details = append(g.Details, parseOpenRouterKey(m)...)
		}
	}
	if len(g.Meters) == 0 {
		return g, errf(model.StatusForbidden, "OpenRouter balance unavailable for this key")
	}
	applySelection(g.Meters, nil)
	return g, nil
}

func parseOpenRouterCredits(m map[string]any) (model.Meter, bool) {
	d := obj(m["data"])
	total, ok1 := num(d["total_credits"])
	used, ok2 := num(d["total_usage"])
	if !ok1 || !ok2 {
		return model.Meter{}, false
	}
	remaining := total - used
	if remaining < 0 {
		remaining = 0
	}
	pct := 100.0
	if total > 0 {
		pct = clampPct(used / total * 100)
	}
	return model.Meter{
		ID: "credits", Kind: model.KindCredit, Label: "Credits", Percent: pct,
		Remaining: model.F(remaining), Total: model.F(total), Currency: "USD",
	}, true
}

func parseOpenRouterKey(m map[string]any) []model.Detail {
	d := obj(m["data"])
	if d == nil {
		return nil
	}
	limit, ok := num(d["limit"])
	if !ok { // null limit means the key has no cap
		return []model.Detail{{Label: "Key limit", Value: "no cap"}}
	}
	v := fmt.Sprintf("$%.2f", limit)
	if r := str(d["limit_reset"]); r != "" {
		v += " " + r
	}
	if rem, ok := num(d["limit_remaining"]); ok {
		v += fmt.Sprintf(", $%.2f left", rem)
	}
	if eb := obj(d["effective_budget"]); eb != nil {
		if t := parseTime(eb["resets_at"]); t != nil {
			v += ", resets " + t.Local().Format("2006-01-02")
		}
	}
	return []model.Detail{{Label: "Key limit", Value: v}}
}

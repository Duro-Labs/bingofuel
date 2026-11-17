package providers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/fernandoduro/bingofuel/internal/config"
	"github.com/fernandoduro/bingofuel/internal/model"
)

// Both paths return the same payload (checked 2026-11-17); wham is what the
// ChatGPT apps use today, codex/usage is the older CLI path.
var codexUsageURLs = []string{
	"https://chatgpt.com/backend-api/wham/usage",
	"https://chatgpt.com/backend-api/codex/usage",
}

type Codex struct {
	Cfg  config.Codex
	URLs []string // test override
}

func (Codex) ID() string            { return "codex" }
func (Codex) Title() string         { return "Codex" }
func (Codex) Color() string         { return "#FFFFFF" }
func (Codex) Local() bool           { return false }
func (c Codex) Selection() []string { return c.Cfg.Meters }

func (c Codex) home() string {
	if c.Cfg.Home != "" {
		return config.Expand(c.Cfg.Home)
	}
	if d := os.Getenv("CODEX_HOME"); d != "" {
		return d
	}
	return filepath.Join(homeDir(), ".codex")
}

func (c Codex) CredentialSource() (string, error) {
	_, err := c.token(time.Now())
	return filepath.Join(c.home(), "auth.json"), err
}

func (c Codex) token(now time.Time) (string, error) {
	path := filepath.Join(c.home(), "auth.json")
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return "", errf(model.StatusNotConfigured, "no Codex login found at %s", path)
	}
	var a struct {
		Tokens struct {
			AccessToken string `json:"access_token"`
		} `json:"tokens"`
	}
	if json.Unmarshal(data, &a) != nil || a.Tokens.AccessToken == "" {
		return "", errf(model.StatusNotConfigured, "%s has no ChatGPT login (API-key mode has no plan limits)", path)
	}
	if exp := jwtExp(a.Tokens.AccessToken); !exp.IsZero() && now.After(exp) {
		return "", errf(model.StatusAuthExpired, "Codex login expired: run codex to refresh it")
	}
	return a.Tokens.AccessToken, nil
}

// jwtExp reads the exp claim without verifying the signature; it is only
// used to avoid sending a token we already know is expired.
func jwtExp(tok string) time.Time {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return time.Time{}
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}
	}
	var claims struct {
		Exp float64 `json:"exp"`
	}
	if json.Unmarshal(payload, &claims) != nil || claims.Exp <= 0 {
		return time.Time{}
	}
	return time.Unix(int64(claims.Exp), 0)
}

func (c Codex) Fetch(ctx context.Context, now time.Time) (model.Group, error) {
	g := model.Group{ID: c.ID(), Title: c.Title(), Color: c.Color()}
	tok, err := c.token(now)
	if err != nil {
		return g, err
	}
	ver := cliVersion("codex", "0.169.0")
	headers := map[string]string{
		"Authorization": "Bearer " + tok,
		// Required in Sept 2026 (403 without them), not on 2026-11-17. Sent anyway.
		"originator": "codex_cli_rs",
		"User-Agent": fmt.Sprintf("codex_cli_rs/%s (%s; %s) terminal", ver, runtime.GOOS, runtime.GOARCH),
	}
	urls := c.URLs
	if len(urls) == 0 {
		urls = codexUsageURLs
	}
	var body []byte
	for _, u := range urls {
		var code int
		body, code, err = get(ctx, u, headers, "Codex")
		if code != 404 {
			break
		}
	}
	if err != nil {
		if s, _ := StatusOf(err); s == model.StatusAuthExpired {
			return g, errf(model.StatusAuthExpired, "Codex rejected the login: run codex to refresh it")
		}
		return g, err
	}
	m, err := decode(body, "Codex")
	if err != nil {
		return g, err
	}
	g.Plan, g.Meters, g.Details = parseCodex(m, now)
	if len(g.Meters) == 0 {
		return g, errf(model.StatusPlanLapsed, "Codex reported no usage windows: is the plan still active?")
	}
	applySelection(g.Meters, c.Cfg.Meters)
	return g, nil
}

// parseCodex copies only allow-listed fields. The payload also carries
// email, user_id and account_id, which must never leave this function.
func parseCodex(m map[string]any, now time.Time) (string, []model.Meter, []model.Detail) {
	var meters []model.Meter
	seen := map[string]bool{}
	add := func(prefix, name string, w map[string]any) {
		if w == nil {
			return
		}
		pct, ok := num(w["used_percent"])
		if !ok {
			return
		}
		secs, _ := num(w["limit_window_seconds"])
		id, label := windowLabel(secs)
		if name != "" {
			label = name
		}
		if id == "" {
			id = "window"
		}
		if prefix != "" {
			id = prefix + "_" + id
		}
		if label == "" {
			label = titleWords(id)
		}
		base := id
		for n := 2; seen[id]; n++ {
			id = fmt.Sprintf("%s_%d", base, n)
		}
		seen[id] = true
		var resets *time.Time
		if t := parseTime(w["reset_at"]); t != nil {
			resets = t
		} else if after, ok := num(w["reset_after_seconds"]); ok && after >= 0 {
			resets = model.T(now.Add(time.Duration(after) * time.Second))
		}
		meters = append(meters, model.Meter{ID: id, Kind: model.KindWindow, Label: label, Percent: clampPct(pct), ResetsAt: resets})
	}

	// Windows are placed by their length, never by slot: a plan without a
	// 5-hour window must not have its weekly one shown as "5-Hour".
	rl := obj(m["rate_limit"])
	add("", "", obj(rl["primary_window"]))
	add("", "", obj(rl["secondary_window"]))

	cr := obj(m["code_review_rate_limit"])
	if _, direct := cr["used_percent"]; direct {
		add("code_review", "Code Review", cr)
	} else {
		add("code_review", "Code Review", obj(cr["primary_window"]))
		add("code_review", "Code Review", obj(cr["secondary_window"]))
	}

	additional := list(m["additional_rate_limits"])
	if a := obj(m["additional_rate_limits"]); a != nil {
		keys := make([]string, 0, len(a))
		for k := range a {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			additional = append(additional, a[k])
		}
	}
	for _, raw := range additional {
		it := obj(raw)
		name := str(it["limit_name"])
		if name == "" {
			name = str(it["name"])
		}
		if inner := obj(it["rate_limit"]); inner != nil {
			add("extra_"+slug(name), name, obj(inner["primary_window"]))
			add("extra_"+slug(name), name, obj(inner["secondary_window"]))
		} else {
			add("extra_"+slug(name), name, it)
		}
	}

	if mu := obj(m["model_usage"]); mu != nil {
		keys := make([]string, 0, len(mu))
		for k := range mu {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			add("model_"+slug(k), k, obj(mu[k]))
		}
	}

	var details []model.Detail
	if c := obj(m["credits"]); c != nil {
		if has, _ := c["has_credits"].(bool); has {
			v := "available"
			if unl, _ := c["unlimited"].(bool); unl {
				v = "unlimited"
			} else if b, ok := num(c["balance"]); ok {
				v = fmt.Sprintf("%.0f credits", b) // Codex credits are not dollars
			} else if b := str(c["balance"]); b != "" {
				v = b + " credits"
			}
			details = append(details, model.Detail{Label: "Credits", Value: v})
		}
	}
	return str(m["plan_type"]), meters, details
}

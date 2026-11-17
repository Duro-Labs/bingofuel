package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"time"

	"github.com/fernandoduro/bingofuel/internal/config"
	"github.com/fernandoduro/bingofuel/internal/model"
)

const claudeUsageURL = "https://api.anthropic.com/api/oauth/usage"

type Claude struct {
	Cfg config.Claude
	// URL overrides the endpoint in tests.
	URL string
}

func (Claude) ID() string            { return "claude" }
func (Claude) Title() string         { return "Claude" }
func (Claude) Color() string         { return "#D77757" }
func (Claude) Local() bool           { return false }
func (c Claude) Selection() []string { return c.Cfg.Meters }

func (c Claude) dir() string {
	if c.Cfg.Dir != "" {
		return config.Expand(c.Cfg.Dir)
	}
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	return filepath.Join(homeDir(), ".claude")
}

type claudeCreds struct {
	OAuth struct {
		AccessToken string  `json:"accessToken"`
		ExpiresAt   float64 `json:"expiresAt"` // ms
	} `json:"claudeAiOauth"`
}

// CredentialSource describes the first usable credential, without the token.
func (c Claude) CredentialSource() (string, error) {
	_, src, err := c.token(time.Now())
	return src, err
}

// token returns the first *usable* credential: Claude Code on macOS and the
// Windows desktop app can leave an empty .credentials.json behind.
func (c Claude) token(now time.Time) (string, string, error) {
	type cand struct {
		name string
		read func() ([]byte, error)
	}
	path := filepath.Join(c.dir(), ".credentials.json")
	cands := []cand{{path, func() ([]byte, error) { return os.ReadFile(path) }}}
	if runtime.GOOS == "darwin" {
		cands = append(cands, cand{"macOS Keychain (Claude Code-credentials)", func() ([]byte, error) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return exec.CommandContext(ctx, "security", "find-generic-password", "-s", "Claude Code-credentials", "-w").Output()
		}})
	}
	expired := ""
	for _, cd := range cands {
		data, err := cd.read()
		if err != nil || len(data) == 0 {
			continue
		}
		var cr claudeCreds
		if json.Unmarshal(data, &cr) != nil || cr.OAuth.AccessToken == "" {
			continue // hollow or unreadable; never echo its contents
		}
		if cr.OAuth.ExpiresAt > 0 && now.UnixMilli() > int64(cr.OAuth.ExpiresAt) {
			expired = cd.name
			continue
		}
		return cr.OAuth.AccessToken, cd.name, nil
	}
	if expired != "" {
		return "", expired, errf(model.StatusAuthExpired, "Claude login expired: run claude to refresh it")
	}
	return "", "", errf(model.StatusNotConfigured, "no usable Claude login found in %s", c.dir())
}

func (c Claude) Fetch(ctx context.Context, now time.Time) (model.Group, error) {
	g := model.Group{ID: c.ID(), Title: c.Title(), Color: c.Color()}
	tok, _, err := c.token(now)
	if err != nil {
		return g, err
	}
	url := c.URL
	if url == "" {
		url = claudeUsageURL
	}
	body, _, err := get(ctx, url, map[string]string{
		"Authorization":  "Bearer " + tok,
		"anthropic-beta": "oauth-2025-04-20",
		// Other agents get ineligible_reason "surface" and fewer fields.
		"User-Agent": fmt.Sprintf("claude-cli/%s (external, cli)", cliVersion("claude", "2.1.325")),
	}, "Claude")
	if err != nil {
		if s, _ := StatusOf(err); s == model.StatusAuthExpired {
			return g, errf(model.StatusAuthExpired, "Claude rejected the login: run claude to refresh it")
		}
		return g, err
	}
	m, err := decode(body, "Claude")
	if err != nil {
		return g, err
	}
	g.Meters, g.Details = parseClaude(m)
	if len(g.Meters) == 0 {
		// A subscription that ended reports no windows at all.
		return g, errf(model.StatusPlanLapsed, "Claude reported no usage windows: is the subscription still active?")
	}
	applySelection(g.Meters, c.Cfg.Meters)
	return g, nil
}

// parseClaude reads the normalized limits[] first, then fills gaps from the
// legacy five_hour / seven_day / seven_day_<model> fields.
func parseClaude(m map[string]any) ([]model.Meter, []model.Detail) {
	var meters []model.Meter
	seen := map[string]bool{}
	add := func(id, label string, pct float64, resets *time.Time) {
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		meters = append(meters, model.Meter{ID: id, Kind: model.KindWindow, Label: label, Percent: clampPct(pct), ResetsAt: resets})
	}

	for _, raw := range list(m["limits"]) {
		l := obj(raw)
		if l == nil {
			continue
		}
		pct, ok := num(l["percent"])
		if !ok {
			if pct, ok = num(l["utilization"]); !ok {
				continue
			}
		}
		kind, group := str(l["kind"]), str(l["group"])
		modelName := str(obj(obj(obj(l["scope"])["model"]))["display_name"])
		var id, label string
		switch {
		case kind == "session" || group == "session":
			id, label = "session", "5-Hour"
		case kind == "weekly_all":
			id, label = "weekly", "Weekly"
		case group == "weekly" && modelName != "":
			id, label = "weekly_"+slug(modelName), modelName
		case kind != "":
			id, label = slug(kind), titleWords(kind)
		default:
			id, label = slug(group), titleWords(group)
		}
		add(id, label, pct, parseTime(l["resets_at"]))
	}

	legacy := func(key, id, label string) {
		w := obj(m[key])
		if w == nil {
			return
		}
		if pct, ok := num(w["utilization"]); ok {
			add(id, label, pct, parseTime(w["resets_at"]))
		}
	}
	legacy("five_hour", "session", "5-Hour")
	legacy("seven_day", "weekly", "Weekly")
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if len(key) > len("seven_day_") && key[:len("seven_day_")] == "seven_day_" {
			name := key[len("seven_day_"):]
			legacy(key, "weekly_"+slug(name), titleWords(name))
		}
	}

	var details []model.Detail
	if ex := obj(m["extra_usage"]); ex != nil {
		if on, _ := ex["is_enabled"].(bool); on {
			places := 2.0
			if p, ok := num(ex["decimal_places"]); ok && p >= 0 && p <= 4 {
				places = p
			}
			div := math.Pow(10, places)
			cur := str(ex["currency"])
			if cur == "" {
				cur = "USD"
			}
			used, _ := num(ex["used_credits"])
			v := fmt.Sprintf("%.2f %s used", used/div, cur)
			if lim, ok := num(ex["monthly_limit"]); ok {
				v = fmt.Sprintf("%.2f of %.2f %s", used/div, lim/div, cur)
			}
			details = append(details, model.Detail{Label: "Extra usage", Value: v})
		}
	}
	return meters, details
}

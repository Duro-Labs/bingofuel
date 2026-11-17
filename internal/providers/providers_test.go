package providers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fernandoduro/bingofuel/internal/config"
	"github.com/fernandoduro/bingofuel/internal/model"
)

var now = time.Date(2026, 11, 17, 19, 0, 0, 0, time.UTC)

func fixture(t *testing.T, name string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func byID(ms []model.Meter) map[string]model.Meter {
	out := map[string]model.Meter{}
	for _, m := range ms {
		out[m.ID] = m
	}
	return out
}

func init() {
	// Keep cliVersion from writing into the real cache during tests.
	dir, _ := os.MkdirTemp("", "bf-test-cache")
	os.Setenv("BINGOFUEL_CACHE_DIR", dir)
}

func TestParseClaudeLimits(t *testing.T) {
	meters, details := parseClaude(fixture(t, "claude-limits.json"))
	got := byID(meters)
	want := map[string]struct {
		label string
		pct   float64
	}{
		"session": {"5-Hour", 54}, "weekly": {"Weekly", 45},
		"weekly_fable_5_2": {"Fable 5.2", 61}, "weekly_sonnet": {"Sonnet", 12},
	}
	for id, w := range want {
		m, ok := got[id]
		if !ok {
			t.Fatalf("missing meter %s in %+v", id, meters)
		}
		if m.Label != w.label || m.Percent != w.pct || m.ResetsAt == nil {
			t.Errorf("%s = %+v, want label %q pct %v with reset", id, m, w.label, w.pct)
		}
	}
	if len(meters) != 4 {
		t.Errorf("got %d meters, want 4 (null and unreadable entries skipped): %+v", len(meters), meters)
	}
	if len(details) != 1 || details[0].Value != "12.34 of 50.00 USD" {
		t.Errorf("extra usage detail = %+v", details)
	}
}

func TestParseClaudeLegacyAndNulls(t *testing.T) {
	meters, _ := parseClaude(fixture(t, "claude-legacy.json"))
	got := byID(meters)
	if got["session"].Percent != 10 || got["weekly"].Percent != 20 || got["weekly_opus"].Label != "Opus" {
		t.Errorf("legacy parse = %+v", meters)
	}
	if m, _ := parseClaude(map[string]any{"limits": nil, "five_hour": nil}); len(m) != 0 {
		t.Errorf("nulls should give no meters, got %+v", m)
	}
}

func TestSelectionTopModel(t *testing.T) {
	meters, _ := parseClaude(fixture(t, "claude-limits.json"))
	ApplySelection(meters, []string{"session", "model_weekly"})
	shown := []string{}
	for _, m := range meters {
		if m.Show {
			shown = append(shown, m.ID)
		}
	}
	if strings.Join(shown, ",") != "session,weekly_fable_5_2" {
		t.Errorf("shown = %v, want session + the most-used model weekly (Fable)", shown)
	}
	ApplySelection(meters, nil)
	for _, m := range meters {
		if !m.Show {
			t.Errorf("empty selection should show all, %s hidden", m.ID)
		}
	}
}

func writeCreds(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ".credentials.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestClaudeCredentials(t *testing.T) {
	dir := t.TempDir()
	c := Claude{Cfg: config.Claude{Dir: dir}}

	writeCreds(t, dir, "")
	if _, _, err := c.token(now); !errorStatus(err, model.StatusNotConfigured) {
		t.Errorf("hollow file: err = %v, want not_configured", err)
	}
	writeCreds(t, dir, `{"claudeAiOauth":{"accessToken":"secret-token","expiresAt":1}}`)
	_, _, err := c.token(now)
	if !errorStatus(err, model.StatusAuthExpired) {
		t.Errorf("expired: err = %v", err)
	}
	if err != nil && strings.Contains(err.Error(), "secret-token") {
		t.Error("error message leaks the token")
	}
	writeCreds(t, dir, `{"claudeAiOauth":{"accessToken":"tok","expiresAt":9999999999999}}`)
	if tok, _, err := c.token(now); err != nil || tok != "tok" {
		t.Errorf("valid: tok=%q err=%v", tok, err)
	}
}

func errorStatus(err error, s model.Status) bool {
	got, _ := StatusOf(err)
	return err != nil && got == s
}

func TestClaudeFetchHTTP(t *testing.T) {
	dir := t.TempDir()
	writeCreds(t, dir, `{"claudeAiOauth":{"accessToken":"tok","expiresAt":9999999999999}}`)
	body, _ := os.ReadFile("testdata/claude-limits.json")
	code := 200
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" || r.Header.Get("anthropic-beta") != "oauth-2025-04-20" ||
			!strings.HasPrefix(r.Header.Get("User-Agent"), "claude-cli/") {
			t.Errorf("bad headers: %v", r.Header)
		}
		w.WriteHeader(code)
		if code == 200 {
			w.Write(body)
		} else {
			w.Write([]byte(`{"error":{"message":"tok leaked?"}}`))
		}
	}))
	defer srv.Close()
	c := Claude{Cfg: config.Claude{Dir: dir}, URL: srv.URL}

	g, err := c.Fetch(context.Background(), now)
	if err != nil || len(g.Meters) != 4 {
		t.Fatalf("fetch: %v %+v", err, g)
	}
	for _, tc := range []struct {
		code int
		want model.Status
	}{{401, model.StatusAuthExpired}, {403, model.StatusForbidden}, {429, model.StatusRateLimited}, {500, model.StatusStale}} {
		code = tc.code
		_, err := c.Fetch(context.Background(), now)
		if !errorStatus(err, tc.want) {
			t.Errorf("HTTP %d: err = %v, want %s", tc.code, err, tc.want)
		}
		if err != nil && strings.Contains(err.Error(), "leaked") {
			t.Errorf("HTTP %d: error echoes the body", tc.code)
		}
	}
}

func TestParseCodex(t *testing.T) {
	cases := []struct {
		file string
		plan string
		want map[string]float64
	}{
		{"codex-free.json", "free", map[string]float64{"monthly": 95}},
		{"codex-plus.json", "plus", map[string]float64{"session": 12.5, "weekly": 61, "code_review_weekly": 4, "extra_cloud_tasks_w1d": 30, "model_gpt_5_6_terra_weekly": 44}},
		{"codex-live-shape.json", "plus", map[string]float64{"session": 0, "weekly": 24}},
		// Placed by window length, not by slot: the weekly window is primary here.
		{"codex-weekly-only.json", "pro", map[string]float64{"weekly": 33}},
		{"codex-unreadable.json", "team", map[string]float64{"session": 7}},
	}
	for _, tc := range cases {
		plan, meters, _ := parseCodex(fixture(t, tc.file), now)
		got := byID(meters)
		if plan != tc.plan || len(meters) != len(tc.want) {
			t.Errorf("%s: plan %q, meters %+v", tc.file, plan, meters)
			continue
		}
		for id, pct := range tc.want {
			if m, ok := got[id]; !ok || m.Percent != pct {
				t.Errorf("%s: %s = %+v, want %v", tc.file, id, m, pct)
			}
		}
	}
	_, plus, details := parseCodex(fixture(t, "codex-plus.json"), now)
	if byID(plus)["session"].Label != "5-Hour" || byID(plus)["weekly"].Label != "Weekly" {
		t.Errorf("labels: %+v", plus)
	}
	if len(details) != 1 || details[0].Value != "1250 credits" {
		t.Errorf("credits detail = %+v (must not be rendered as dollars)", details)
	}
}

func TestCodexAllowList(t *testing.T) {
	plan, meters, details := parseCodex(fixture(t, "codex-live-shape.json"), now)
	out, _ := json.Marshal(model.Group{Plan: plan, Meters: meters, Details: details})
	for _, s := range []string{"example.com", "user-FAKE", "acct-FAKE", "email", "user_id", "account_id"} {
		if strings.Contains(string(out), s) {
			t.Errorf("normalized output contains %q: %s", s, out)
		}
	}
}

func fakeJWT(exp int64) string {
	b, _ := json.Marshal(map[string]int64{"exp": exp})
	return "x." + base64.RawURLEncoding.EncodeToString(b) + ".y"
}

func TestCodexFetchFallbackAndExpiry(t *testing.T) {
	home := t.TempDir()
	write := func(tok string) {
		os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{"tokens":{"access_token":"`+tok+`"}}`), 0o600)
	}
	body, _ := os.ReadFile("testdata/codex-plus.json")
	hits := []string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, r.URL.Path)
		if r.Header.Get("originator") != "codex_cli_rs" {
			t.Errorf("missing originator header")
		}
		if r.URL.Path == "/wham" {
			w.WriteHeader(404)
			return
		}
		w.Write(body)
	}))
	defer srv.Close()
	c := Codex{Cfg: config.Codex{Home: home}, URLs: []string{srv.URL + "/wham", srv.URL + "/codex"}}

	write(fakeJWT(now.Add(time.Hour).Unix()))
	g, err := c.Fetch(context.Background(), now)
	if err != nil || g.Plan != "plus" || strings.Join(hits, ",") != "/wham,/codex" {
		t.Errorf("fallback: err=%v plan=%q hits=%v", err, g.Plan, hits)
	}
	write(fakeJWT(now.Add(-time.Hour).Unix()))
	if _, err := c.Fetch(context.Background(), now); !errorStatus(err, model.StatusAuthExpired) {
		t.Errorf("expired JWT: %v", err)
	}
	os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{"OPENAI_API_KEY":"sk-x","tokens":null}`), 0o600)
	if _, err := c.Fetch(context.Background(), now); !errorStatus(err, model.StatusNotConfigured) {
		t.Errorf("API-key mode: %v", err)
	}
}

func TestOpenRouter(t *testing.T) {
	creditsCode := 200
	keyBody := `{"data":{"limit":20,"limit_reset":"monthly","limit_remaining":19.93,"effective_budget":{"resets_at":"2026-12-01T00:00:00.000Z"}}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/credits":
			w.WriteHeader(creditsCode)
			w.Write([]byte(`{"data":{"total_credits":10,"total_usage":0.072728466}}`))
		case "/key":
			w.Write([]byte(keyBody))
		}
	}))
	defer srv.Close()
	o := OpenRouter{Base: srv.URL, Key: "k"}

	g, err := o.Fetch(context.Background(), now)
	if err != nil || len(g.Meters) != 1 {
		t.Fatalf("fetch: %v %+v", err, g)
	}
	m := g.Meters[0]
	if m.Kind != model.KindCredit || *m.Remaining < 9.927 || *m.Remaining > 9.928 || *m.Total != 10 || m.Percent < 0.72 || m.Percent > 0.73 || m.Currency != "USD" {
		t.Errorf("credit meter = %+v", m)
	}
	if len(g.Details) != 1 || !strings.HasPrefix(g.Details[0].Value, "$20.00 monthly, $19.93 left") {
		t.Errorf("key detail = %+v", g.Details)
	}

	keyBody = `{"data":{"limit":null}}`
	g, _ = o.Fetch(context.Background(), now)
	if g.Details[0].Value != "no cap" {
		t.Errorf("null limit = %+v", g.Details)
	}

	creditsCode = 403
	g, err = o.Fetch(context.Background(), now)
	if !errorStatus(err, model.StatusForbidden) || len(g.Meters) != 0 {
		t.Errorf("403 on credits: err=%v group=%+v", err, g)
	}
}

func TestTypeSafeLedger(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "decisions.jsonl")
	cp := now.Add(-time.Hour)
	lines := []string{
		`{"ts": ` + epoch(cp.Add(-time.Minute)) + `, "jev": {"input_tokens": 999999999}}`, // before the checkpoint
		`{"ts": ` + epoch(cp.Add(time.Minute)) + `, "jev": null}`,                         // JEV not consulted
		`{"ts": ` + epoch(cp.Add(2*time.Minute)) + `, "jev": {"input_tokens": 500000}}`,
		`{"ts": "` + cp.Add(3*time.Minute).Format(time.RFC3339) + `", "input_tokens": 500000}`, // generic line
		`not json`,
	}
	os.WriteFile(logPath, []byte(strings.Join(lines, "\n")), 0o600)
	ts := TypeSafe{Cfg: config.TypeSafe{PricePerMTok: 0.042, UsageLogs: []string{logPath}}, LedgerPath: filepath.Join(dir, "ledger.json")}

	if _, err := ts.Fetch(context.Background(), now); !errorStatus(err, model.StatusNotConfigured) {
		t.Errorf("no checkpoint: %v", err)
	}
	if err := ts.SetBalance(5, cp); err != nil {
		t.Fatal(err)
	}
	g, err := ts.Fetch(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	m := g.Meters[0]
	// 1M tokens x $0.042/M = $0.042 spent of $5.
	if !m.Estimate || *m.Total != 5 || *m.Remaining < 4.9579 || *m.Remaining > 4.9581 || m.Percent < 0.83 || m.Percent > 0.85 {
		t.Errorf("meter = %+v remaining %v", m, *m.Remaining)
	}

	os.WriteFile(logPath, []byte(strings.Join(append(lines, `{"ts": `+epoch(cp.Add(4*time.Minute))+`, "jev": {"error": "HTTPError: HTTP Error 402: Payment Required"}}`), "\n")), 0o600)
	g, _ = ts.Fetch(context.Background(), now)
	if *g.Meters[0].Remaining != 0 || g.Meters[0].Percent != 100 {
		t.Errorf("402 should zero the balance: %+v", g.Meters[0])
	}
}

func epoch(t time.Time) string {
	b, _ := json.Marshal(float64(t.UnixNano()) / 1e9)
	return string(b)
}

func TestLedgerRejectsNonFinite(t *testing.T) {
	dir := t.TempDir()
	ts := TypeSafe{LedgerPath: filepath.Join(dir, "ledger.json")}
	if err := ts.SetBalance(5, now); err != nil {
		t.Fatal(err)
	}
	for _, v := range []float64{math.NaN(), math.Inf(1), -1} {
		if err := ts.SetBalance(v, now); err == nil {
			t.Errorf("SetBalance(%v) accepted", v)
		}
	}
	l, err := ts.LoadLedger()
	if err != nil || len(l.Checkpoints) != 1 || l.Checkpoints[0].BalanceUSD != 5 {
		t.Errorf("ledger damaged: %+v %v", l, err)
	}
}

func TestBillingMatch(t *testing.T) {
	u := Usage{}
	scanLine([]byte(`{"ts": 1894000000, "jev": {"error": "timeout after 4020 ms"}}`), now, &u)
	if u.SawBilling {
		t.Error("4020 ms timeout read as a 402")
	}
	scanLine([]byte(`{"ts": 1894000000, "jev": {"error": "HTTPError: HTTP Error 402: Payment Required"}}`), now, &u)
	if !u.SawBilling {
		t.Error("402 missed")
	}
}

func TestFractionalEpochKeepsSubSecond(t *testing.T) {
	cp := time.Unix(1794933451, 100_000_000)
	u := Usage{}
	scanLine([]byte(`{"ts": 1794933451.424, "jev": {"input_tokens": 10}}`), cp, &u)
	if u.Calls != 1 {
		t.Error("a call 0.3 s after the checkpoint, in the same second, was dropped")
	}
}

func TestNoWindowsMeansPlanLapsed(t *testing.T) {
	dir := t.TempDir()
	writeCreds(t, dir, `{"claudeAiOauth":{"accessToken":"tok","expiresAt":9999999999999}}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"five_hour": null, "seven_day": null, "limits": []}`))
	}))
	defer srv.Close()
	_, err := Claude{Cfg: config.Claude{Dir: dir}, URL: srv.URL}.Fetch(context.Background(), now)
	if !errorStatus(err, model.StatusPlanLapsed) {
		t.Errorf("err = %v, want plan_lapsed", err)
	}
}

func TestWindowLabel(t *testing.T) {
	for secs, want := range map[float64]string{18000: "5-Hour", 604800: "Weekly", 2592000: "Monthly", 86400: "1d", 7200: "2h", 0: ""} {
		if _, got := windowLabel(secs); got != want {
			t.Errorf("windowLabel(%v) = %q, want %q", secs, got, want)
		}
	}
}

// Package providers turns each service's usage API into model.Group values.
// Every parser is lenient: a field it cannot read is skipped, never guessed,
// and nothing outside an explicit allow-list leaves this package.
package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/fernandoduro/bingofuel/internal/config"
	"github.com/fernandoduro/bingofuel/internal/model"
)

type Provider interface {
	ID() string
	Title() string
	Color() string
	// Local providers compute from local files only; the engine never caches them.
	Local() bool
	// Selection lists the meter ids shown as rings; nil shows all.
	Selection() []string
	Fetch(ctx context.Context, now time.Time) (model.Group, error)
}

// Error carries the status a failed fetch maps to and a message safe to show.
// Messages never include response bodies or credential contents.
type Error struct {
	Status  model.Status
	Message string
}

func (e *Error) Error() string { return string(e.Status) + ": " + e.Message }

func errf(s model.Status, format string, a ...any) error {
	return &Error{Status: s, Message: fmt.Sprintf(format, a...)}
}

// StatusOf maps any error to a status; unknown errors are transient.
func StatusOf(err error) (model.Status, string) {
	var pe *Error
	if errors.As(err, &pe) {
		return pe.Status, pe.Message
	}
	return model.StatusStale, "fetch failed"
}

var httpClient = &http.Client{Timeout: 10 * time.Second}

const maxBody = 4 << 20

// get performs a GET and maps HTTP failures to statuses. The token goes in a
// header built in-process, so it never appears in argv or logs.
func get(ctx context.Context, url string, headers map[string]string, service string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Accept", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, 0, errf(model.StatusStale, "could not reach %s", service)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, resp.StatusCode, errf(model.StatusStale, "%s response was cut off", service)
	}
	switch {
	case resp.StatusCode == http.StatusOK:
		return body, resp.StatusCode, nil
	case resp.StatusCode == http.StatusUnauthorized:
		return nil, resp.StatusCode, errf(model.StatusAuthExpired, "%s rejected the login (401)", service)
	case resp.StatusCode == http.StatusForbidden:
		return nil, resp.StatusCode, errf(model.StatusForbidden, "%s refused access (403)", service)
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, resp.StatusCode, errf(model.StatusRateLimited, "%s is rate limiting (429)", service)
	default:
		return nil, resp.StatusCode, errf(model.StatusStale, "%s answered HTTP %d", service, resp.StatusCode)
	}
}

// decode parses JSON without ever echoing the input in the error.
func decode(body []byte, service string) (map[string]any, error) {
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil || m == nil {
		return nil, errf(model.StatusError, "%s sent a response that is not a JSON object", service)
	}
	return m, nil
}

// --- lenient accessors: wrong type or null reads as absent ---

func obj(v any) map[string]any { m, _ := v.(map[string]any); return m }
func list(v any) []any         { l, _ := v.([]any); return l }
func str(v any) string         { s, _ := v.(string); return s }

func num(v any) (float64, bool) {
	f, ok := v.(float64) // encoding/json decodes every number as float64; bools fail here
	return f, ok
}

func parseTime(v any) *time.Time {
	switch t := v.(type) {
	case string:
		if t == "" {
			return nil
		}
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
			if p, err := time.Parse(layout, t); err == nil {
				return &p
			}
		}
	case float64:
		if t > 0 && !math.IsInf(t, 0) {
			sec := math.Floor(t)
			p := time.Unix(int64(sec), int64((t-sec)*1e9))
			return &p
		}
	}
	return nil
}

func clampPct(p float64) float64 {
	switch {
	case p < 0:
		return 0
	case p > 100:
		return 100
	}
	return p
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func slug(s string) string {
	return strings.Trim(slugRe.ReplaceAllString(strings.ToLower(s), "_"), "_")
}

func titleWords(s string) string {
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == '_' || r == '-' || r == ' ' })
	for i, p := range parts {
		if p != "" {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}

// windowLabel names a quota window by its length.
func windowLabel(seconds float64) (id, label string) {
	switch int64(seconds) {
	case 18000:
		return "session", "5-Hour"
	case 604800:
		return "weekly", "Weekly"
	case 2592000:
		return "monthly", "Monthly"
	}
	if seconds <= 0 {
		return "", ""
	}
	h := seconds / 3600
	if h < 24 {
		return fmt.Sprintf("w%dh", int64(h+0.5)), fmt.Sprintf("%dh", int64(h+0.5))
	}
	d := int64(h/24 + 0.5)
	return fmt.Sprintf("w%dd", d), fmt.Sprintf("%dd", d)
}

// applySelection marks the meters chosen as rings. "model_weekly" selects the
// most-used model-scoped weekly meter. An empty selection shows everything.
func applySelection(meters []model.Meter, selected []string) { ApplySelection(meters, selected) }

// ApplySelection sets Show on each meter from a selection list (see above).
func ApplySelection(meters []model.Meter, selected []string) {
	for i := range meters {
		meters[i].Show = false
	}
	if len(selected) == 0 {
		for i := range meters {
			meters[i].Show = true
		}
		return
	}
	want := map[string]bool{}
	for _, s := range selected {
		want[s] = true
	}
	top := -1
	for i, m := range meters {
		if want[m.ID] {
			meters[i].Show = true
		}
		if strings.HasPrefix(m.ID, "weekly_") && (top < 0 || m.Percent > meters[top].Percent) {
			top = i
		}
	}
	if want["model_weekly"] && top >= 0 {
		meters[top].Show = true
	}
}

// --- installed CLI version, for user-agents that must look like the CLI ---

var versionRe = regexp.MustCompile(`\d+\.\d+\.\d+`)

var versionMu sync.Mutex

// cliVersion runs `<bin> --version` at most once a day and caches the result.
func cliVersion(bin, fallback string) string {
	versionMu.Lock()
	defer versionMu.Unlock()
	dir, err := config.CacheDir()
	if err != nil {
		return fallback
	}
	path := filepath.Join(dir, "version-"+bin)
	if st, err := os.Stat(path); err == nil && time.Since(st.ModTime()) < 24*time.Hour {
		if b, err := os.ReadFile(path); err == nil && versionRe.Match(b) {
			return strings.TrimSpace(string(b))
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "--version").Output()
	v := versionRe.FindString(string(out))
	if err != nil || v == "" {
		return fallback
	}
	_ = config.WriteFileAtomic(path, []byte(v), 0o600)
	return v
}

func homeDir() string {
	h, _ := os.UserHomeDir()
	return h
}

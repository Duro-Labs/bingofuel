package providers

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fernandoduro/bingofuel/internal/config"
	"github.com/fernandoduro/bingofuel/internal/model"
)

// TypeSafe has no balance endpoint (its API is /v1/models and /v1/systemone
// only). The balance is a local ledger: the amount the user typed at the last
// top-up, minus input tokens x price for every JEV call found in usage logs.
type TypeSafe struct {
	Cfg config.TypeSafe
	// LedgerPath overrides the ledger location in tests.
	LedgerPath string
}

func (TypeSafe) ID() string          { return "typesafe" }
func (TypeSafe) Title() string       { return "JEV" }
func (TypeSafe) Color() string       { return "#D45BB6" }
func (TypeSafe) Local() bool         { return true }
func (TypeSafe) Selection() []string { return nil }

type Checkpoint struct {
	BalanceUSD float64   `json:"balance_usd"`
	At         time.Time `json:"at"`
}

type Ledger struct {
	Checkpoints []Checkpoint `json:"checkpoints"`
}

func (t TypeSafe) ledgerPath() (string, error) {
	if t.LedgerPath != "" {
		return t.LedgerPath, nil
	}
	dir, err := config.StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "jev-ledger.json"), nil
}

func (t TypeSafe) LoadLedger() (Ledger, error) {
	var l Ledger
	p, err := t.ledgerPath()
	if err != nil {
		return l, err
	}
	data, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return l, nil
	}
	if err != nil {
		return l, err
	}
	if json.Unmarshal(data, &l) != nil {
		return Ledger{}, fmt.Errorf("%s is not a valid ledger", p)
	}
	return l, nil
}

// SetBalance records the balance the user just read off the TypeSafe console.
func (t TypeSafe) SetBalance(usd float64, at time.Time) error {
	if usd < 0 {
		return errors.New("balance cannot be negative")
	}
	l, err := t.LoadLedger()
	if err != nil {
		return err
	}
	l.Checkpoints = append(l.Checkpoints, Checkpoint{BalanceUSD: usd, At: at.UTC()})
	if n := len(l.Checkpoints); n > 50 {
		l.Checkpoints = l.Checkpoints[n-50:]
	}
	p, err := t.ledgerPath()
	if err != nil {
		return err
	}
	data, _ := json.MarshalIndent(l, "", "  ")
	return config.WriteFileAtomic(p, data, 0o600)
}

// Usage is what the logs say happened since a checkpoint.
type Usage struct {
	Calls       int
	InputTokens int64
	SawBilling  bool // a 402 / billing error: the balance is empty
	Unreadable  []string
}

func (t TypeSafe) ScanUsage(since time.Time) Usage {
	var u Usage
	for _, p := range t.Cfg.UsageLogs {
		path := config.Expand(p)
		f, err := os.Open(path)
		if err != nil {
			u.Unreadable = append(u.Unreadable, path)
			continue
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64*1024), 8<<20)
		for sc.Scan() {
			scanLine(sc.Bytes(), since, &u)
		}
		f.Close()
	}
	return u
}

// scanLine understands the Hermes delegation-tiers decision log
// ({"ts": <epoch>, "jev": {"input_tokens": N} | {"error": "..."}}) and a
// generic {"ts": <epoch|RFC3339>, "input_tokens": N} line.
func scanLine(line []byte, since time.Time, u *Usage) {
	var rec map[string]any
	if json.Unmarshal(line, &rec) != nil {
		return
	}
	ts := parseTime(rec["ts"])
	if ts == nil {
		if f, ok := num(rec["ts"]); ok { // fractional epoch
			t := time.Unix(int64(f), int64((f-float64(int64(f)))*1e9))
			ts = &t
		}
	}
	if ts == nil || !ts.After(since) {
		return
	}
	src := rec
	if j := obj(rec["jev"]); j != nil {
		src = j
	} else if _, ok := rec["jev"]; ok {
		return // jev: null, JEV was not consulted
	}
	if e := strings.ToLower(str(src["error"])); e != "" {
		if strings.Contains(e, "402") || strings.Contains(e, "billing") || strings.Contains(e, "payment required") {
			u.SawBilling = true
		}
		return
	}
	if n, ok := num(src["input_tokens"]); ok && n > 0 {
		u.Calls++
		u.InputTokens += int64(n)
	}
}

func (t TypeSafe) Fetch(_ context.Context, now time.Time) (model.Group, error) {
	g := model.Group{ID: t.ID(), Title: t.Title(), Color: t.Color()}
	l, err := t.LoadLedger()
	if err != nil {
		return g, errf(model.StatusError, "%v", err)
	}
	if len(l.Checkpoints) == 0 {
		return g, errf(model.StatusNotConfigured, "no JEV balance entered: run bingofuel jev balance <usd>")
	}
	cp := l.Checkpoints[len(l.Checkpoints)-1]
	u := t.ScanUsage(cp.At)
	spent := float64(u.InputTokens) / 1e6 * t.Cfg.PricePerMTok
	remaining := cp.BalanceUSD - spent
	if remaining < 0 || u.SawBilling {
		remaining = 0
	}
	pct := 100.0
	if cp.BalanceUSD > 0 {
		pct = clampPct((cp.BalanceUSD - remaining) / cp.BalanceUSD * 100)
	}
	g.Meters = []model.Meter{{
		ID: "credits", Kind: model.KindCredit, Label: "Credits", Show: true, Percent: pct,
		Remaining: model.F(remaining), Total: model.F(cp.BalanceUSD), Currency: "USD", Estimate: true,
	}}
	g.Details = []model.Detail{
		{Label: "Balance entered", Value: fmt.Sprintf("$%.2f on %s", cp.BalanceUSD, cp.At.Local().Format("2006-01-02 15:04"))},
		{Label: "Since then", Value: fmt.Sprintf("%d calls, %d input tokens ≈ $%.4f", u.Calls, u.InputTokens, spent)},
	}
	if u.SawBilling {
		g.Details = append(g.Details, model.Detail{Label: "Warning", Value: "a JEV call failed with a billing error: balance is empty"})
	}
	if len(u.Unreadable) > 0 {
		g.Details = append(g.Details, model.Detail{Label: "Unreadable logs", Value: strings.Join(u.Unreadable, ", ")})
	}
	return g, nil
}

// CheckTypeSafeKey calls the free /v1/models endpoint to validate a key.
func CheckTypeSafeKey(ctx context.Context, key string) error {
	_, _, err := get(ctx, "https://api.typesafe.ai/v1/models", map[string]string{"Authorization": "Bearer " + key}, "TypeSafe")
	return err
}

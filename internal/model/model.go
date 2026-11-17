// Package model defines the JSON that every Bingo Fuel front-end reads.
// Front-ends only draw; they never see provider field names.
package model

import "time"

// SchemaVersion is bumped on any breaking change to Output.
const SchemaVersion = 1

type Status string

const (
	StatusOK            Status = "ok"
	StatusStale         Status = "stale"          // last fetch failed for a transient reason; figures are old
	StatusRateLimited   Status = "rate_limited"   // provider said 429; figures held as current
	StatusAuthExpired   Status = "auth_expired"   // 401 or expired token: re-login fixes it
	StatusForbidden     Status = "forbidden"      // 403: re-login does not fix it
	StatusPlanLapsed    Status = "plan_lapsed"    // the account no longer has the plan; old figures dropped
	StatusNotConfigured Status = "not_configured" // credentials or setup missing
	StatusError         Status = "error"
)

// Dimmed reports whether a front-end should draw this group's rings as stale.
func (s Status) Dimmed() bool { return s != StatusOK && s != StatusRateLimited }

type Output struct {
	Schema    int       `json:"schema"`
	FetchedAt time.Time `json:"fetched_at"`
	// ConfigFound is false when no config file exists and defaults were used.
	ConfigFound bool `json:"config_found"`
	// Error is set when the whole run failed (e.g. an unreadable config).
	Error  string  `json:"error,omitempty"`
	Groups []Group `json:"groups"`
}

type Group struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Color   string `json:"color"` // default brand colour; front-ends may override
	Plan    string `json:"plan,omitempty"`
	Status  Status `json:"status"`
	Message string `json:"message,omitempty"`
	// LastSuccess is when this provider last answered with usable data.
	LastSuccess *time.Time `json:"last_success,omitempty"`
	Meters      []Meter    `json:"meters"`
	Details     []Detail   `json:"details,omitempty"`
}

const (
	KindWindow = "window" // a rolling quota window: percent used + reset time
	KindCredit = "credit" // a prepaid balance: percent used of total + amount left
)

type Meter struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Label string `json:"label"`
	// Show marks the meters the user selected as rings; the popup lists all.
	Show     bool       `json:"show"`
	Percent  float64    `json:"percent"` // 0-100, share used
	ResetsAt *time.Time `json:"resets_at,omitempty"`
	// Credit meters only.
	Remaining *float64 `json:"remaining,omitempty"`
	Total     *float64 `json:"total,omitempty"`
	Currency  string   `json:"currency,omitempty"` // ISO code, e.g. USD; never implied
	Estimate  bool     `json:"estimate,omitempty"` // value is a local estimate, draw with "≈"
}

type Detail struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

func F(v float64) *float64 { return &v }

func T(t time.Time) *time.Time { return &t }

package cli

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/fernandoduro/bingofuel/internal/model"
)

func cmdStatus(_ []string) int {
	out, _ := run(-1)
	color := isTerminal(os.Stdout) && os.Getenv("NO_COLOR") == ""
	renderStatus(os.Stdout, out, time.Now(), color)
	return 0
}

func renderStatus(w io.Writer, out model.Output, now time.Time, color bool) {
	paint := func(hex, s string) string {
		if !color {
			return s
		}
		r, g, b, ok := rgb(hex)
		if !ok {
			return s
		}
		return fmt.Sprintf("\x1b[38;2;%d;%d;%dm%s\x1b[0m", r, g, b, s)
	}
	dim := func(s string) string {
		if !color {
			return s
		}
		return "\x1b[2m" + s + "\x1b[0m"
	}
	if out.Error != "" {
		fmt.Fprintln(w, "error:", out.Error)
	}
	if !out.ConfigFound {
		fmt.Fprintln(w, dim("No config yet: using defaults. Run `bingofuel setup` to choose providers."))
	}
	for i, g := range out.Groups {
		if i > 0 {
			fmt.Fprintln(w)
		}
		head := g.Title
		if g.Plan != "" {
			head += " · " + g.Plan
		}
		fmt.Fprintln(w, paint(g.Color, head))
		if g.Status != model.StatusOK {
			msg := g.Message
			if msg == "" {
				msg = string(g.Status)
			}
			if g.LastSuccess != nil {
				msg += fmt.Sprintf(" (last good data %s ago)", Ago(now.Sub(*g.LastSuccess)))
			}
			fmt.Fprintln(w, "  ! "+msg)
		}
		for _, m := range g.Meters {
			bar := Bar(m.Percent, 20)
			var line string
			if m.Kind == model.KindCredit && m.Remaining != nil {
				approx := ""
				if m.Estimate {
					approx = "≈"
				}
				line = fmt.Sprintf("  %-12s %s %5.1f%% used  %s%s left", m.Label, paint(g.Color, bar), m.Percent, approx, Money(*m.Remaining, m.Currency))
			} else {
				line = fmt.Sprintf("  %-12s %s %5.0f%%", m.Label, paint(g.Color, bar), m.Percent)
				if m.ResetsAt != nil {
					line += "  resets in " + Countdown(m.ResetsAt.Sub(now))
				}
			}
			if !m.Show {
				line = dim(line + "  (not shown as a ring)")
			} else if g.Status.Dimmed() {
				line = dim(line)
			}
			fmt.Fprintln(w, line)
		}
		for _, d := range g.Details {
			fmt.Fprintln(w, dim(fmt.Sprintf("  %s: %s", d.Label, d.Value)))
		}
	}
}

func Bar(pct float64, width int) string {
	n := int(pct/100*float64(width) + 0.5)
	if n > width {
		n = width
	}
	if n < 0 {
		n = 0
	}
	return strings.Repeat("█", n) + strings.Repeat("░", width-n)
}

// Countdown matches the widget: HH:MM under a day, "Nd Nh" from there.
func Countdown(d time.Duration) string {
	if d <= 0 {
		return "00:00"
	}
	mins := int(d.Minutes())
	h := mins / 60
	if h >= 24 {
		return fmt.Sprintf("%dd %dh", h/24, h%24)
	}
	return fmt.Sprintf("%02d:%02d", h, mins%60)
}

func Ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "under a minute"
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%.1f h", d.Hours())
	}
	return fmt.Sprintf("%d days", int(d.Hours()/24))
}

func Money(v float64, currency string) string {
	if currency == "USD" || currency == "" {
		return fmt.Sprintf("$%.2f", v)
	}
	return fmt.Sprintf("%.2f %s", v, currency)
}

func rgb(hex string) (uint8, uint8, uint8, bool) {
	hex = strings.TrimPrefix(hex, "#")
	if len(hex) != 6 {
		return 0, 0, 0, false
	}
	v, err := strconv.ParseUint(hex, 16, 32)
	if err != nil {
		return 0, 0, 0, false
	}
	return uint8(v >> 16), uint8(v >> 8), uint8(v), true
}

func isTerminal(f *os.File) bool {
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

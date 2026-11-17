// Package cli implements the bingofuel commands.
package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/fernandoduro/bingofuel/internal/config"
	"github.com/fernandoduro/bingofuel/internal/engine"
	"github.com/fernandoduro/bingofuel/internal/model"
	"github.com/fernandoduro/bingofuel/internal/providers"
	"github.com/fernandoduro/bingofuel/internal/secrets"
	"golang.org/x/term"
)

var Version = "dev"

const usage = `Bingo Fuel: how much AI quota and credit you have left.

Usage:
  bingofuel fetch [--max-age SECONDS] [--pretty]   JSON for front-ends (always exits 0)
  bingofuel status                                 human-readable summary
  bingofuel setup                                  interactive setup wizard
  bingofuel doctor                                 check every provider and explain problems
  bingofuel jev balance <USD>                      record the TypeSafe balance after a top-up
  bingofuel jev show                               show the JEV ledger
  bingofuel secret set <openrouter|typesafe>       store an API key read from stdin
  bingofuel secret delete <openrouter|typesafe>
  bingofuel version
`

func Main(args []string) int {
	if len(args) == 0 {
		fmt.Print(usage)
		return 2
	}
	switch args[0] {
	case "fetch":
		return cmdFetch(args[1:])
	case "status":
		return cmdStatus(args[1:])
	case "setup":
		return cmdSetup(args[1:])
	case "doctor":
		return cmdDoctor(args[1:])
	case "jev":
		return cmdJEV(args[1:])
	case "secret":
		return cmdSecret(args[1:])
	case "version", "--version", "-v":
		fmt.Println("bingofuel", Version)
		return 0
	case "help", "--help", "-h":
		fmt.Print(usage)
		return 0
	}
	fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", args[0], usage)
	return 2
}

// run loads config and runs the engine. maxAge < 0 keeps the configured floor.
func run(maxAge int) (model.Output, config.Config) {
	cfg, err := config.Load()
	if err != nil {
		return model.Output{Schema: model.SchemaVersion, FetchedAt: time.Now(), Error: err.Error(), Groups: []model.Group{}}, cfg
	}
	e, err := engine.New(cfg)
	if err != nil {
		return model.Output{Schema: model.SchemaVersion, FetchedAt: time.Now(), Error: err.Error(), Groups: []model.Group{}}, cfg
	}
	if maxAge >= 0 {
		e.MaxAge = time.Duration(maxAge) * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	return e.Run(ctx), cfg
}

func cmdFetch(args []string) int {
	fs := flag.NewFlagSet("fetch", flag.ContinueOnError)
	maxAge := fs.Int("max-age", -1, "serve cached figures younger than this many seconds (default: config, 300)")
	pretty := fs.Bool("pretty", false, "indent the JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	out, _ := run(*maxAge)
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	if *pretty {
		enc.SetIndent("", "  ")
	}
	_ = enc.Encode(out)
	return 0
}

func cmdJEV(args []string) int {
	cfg, _ := config.Load()
	ts := providers.TypeSafe{Cfg: cfg.TypeSafe}
	if len(args) == 2 && args[0] == "balance" {
		v, err := parseUSD(args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, "balance must be a non-negative number of US dollars, e.g. 4.98")
			return 2
		}
		if err := ts.SetBalance(v, time.Now()); err != nil {
			fmt.Fprintln(os.Stderr, "could not save:", err)
			return 1
		}
		fmt.Printf("JEV balance set to $%.2f as of now.\n", v)
		return 0
	}
	if len(args) == 1 && args[0] == "show" {
		l, err := ts.LoadLedger()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		if len(l.Checkpoints) == 0 {
			fmt.Println("No balance entered yet. Run: bingofuel jev balance <USD>")
			return 0
		}
		for _, c := range l.Checkpoints {
			fmt.Printf("%s  $%.2f\n", c.At.Local().Format("2006-01-02 15:04"), c.BalanceUSD)
		}
		g, err := ts.Fetch(context.Background(), time.Now())
		if err == nil {
			for _, d := range g.Details {
				fmt.Printf("%s: %s\n", d.Label, d.Value)
			}
			fmt.Printf("Estimated remaining: ≈$%.2f\n", *g.Meters[0].Remaining)
		}
		return 0
	}
	fmt.Fprint(os.Stderr, usage)
	return 2
}

// parseUSD accepts "4.98" or "$4.98"; rejects negatives, NaN and Inf, which
// strconv.ParseFloat would otherwise accept.
func parseUSD(s string) (float64, error) {
	v, err := strconv.ParseFloat(strings.TrimPrefix(strings.TrimSpace(s), "$"), 64)
	if err != nil || v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, fmt.Errorf("not an amount")
	}
	return v, nil
}

func cmdSecret(args []string) int {
	if len(args) != 2 || (args[1] != "openrouter" && args[1] != "typesafe") {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	switch args[0] {
	case "set":
		var b []byte
		var err error
		if isTerminal(os.Stdin) { // typed by hand: don't echo the key
			fmt.Fprint(os.Stderr, args[1]+" API key (hidden): ")
			b, err = term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Fprintln(os.Stderr)
		} else {
			b, err = io.ReadAll(io.LimitReader(os.Stdin, 4096))
		}
		v := strings.TrimSpace(string(b))
		if err != nil || v == "" {
			fmt.Fprintln(os.Stderr, "no key on stdin")
			return 2
		}
		src, err := secrets.Set(args[1], v)
		if err != nil {
			fmt.Fprintln(os.Stderr, "could not store the key:", err)
			return 1
		}
		fmt.Printf("%s key stored in the %s.\n", args[1], describeSource(src))
		invalidate(args[1])
		return 0
	case "delete":
		secrets.Delete(args[1])
		invalidate(args[1])
		fmt.Printf("%s key deleted.\n", args[1])
		return 0
	}
	fmt.Fprint(os.Stderr, usage)
	return 2
}

func invalidate(id string) {
	cfg, _ := config.Load()
	if e, err := engine.New(cfg); err == nil {
		e.Invalidate(id)
	}
}

func describeSource(s secrets.Source) string {
	switch s {
	case secrets.SourceKeyring:
		return "OS keyring"
	case secrets.SourceFile:
		return "fallback file (no keyring available)"
	case secrets.SourceEnv:
		return "environment"
	}
	return "nowhere"
}

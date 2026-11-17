// Command bingofuel reports how much AI subscription quota and prepaid
// credit is left. See `bingofuel help`.
package main

import (
	"os"

	"github.com/fernandoduro/bingofuel/internal/cli"
)

var version = "dev"

func main() {
	cli.Version = version
	os.Exit(cli.Main(os.Args[1:]))
}

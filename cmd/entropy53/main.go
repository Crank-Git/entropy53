// Command entropy53 is the CLI for drand rounds served over DNS.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/Crank-Git/entropy53/internal/version"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run parses args, writes the result to out and returns the exit code.
// The flag package writes its own error and the usage to errOut, so run does not repeat them.
// A help request prints the usage once and returns 0. A bad flag returns 2.
func run(args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("entropy53", flag.ContinueOnError)
	fs.SetOutput(errOut)
	showVersion := fs.Bool("version", false, "print the build version and exit")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *showVersion {
		if _, err := fmt.Fprintln(out, version.String()); err != nil {
			// The write to out failed, so a message to errOut is the only report left.
			_, _ = fmt.Fprintln(errOut, err)
			return 2
		}
	}
	return 0
}

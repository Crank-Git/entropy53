// Command entropy53 is the CLI for drand rounds served over DNS.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/Crank-Git/entropy53/internal/version"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run is runWith with the wall clock.
func run(args []string, out, errOut io.Writer) int {
	return runWith(args, out, errOut, time.Now)
}

// runWith parses args, writes the result to out and returns the exit code.
// The flag package writes its own error and the usage to errOut, so runWith does not repeat them.
// A help request prints the usage once and returns 0. A bad flag, a missing command or an unknown command returns 2.
func runWith(args []string, out, errOut io.Writer, now func() time.Time) int {
	fs := flag.NewFlagSet("entropy53", flag.ContinueOnError)
	fs.SetOutput(errOut)
	fs.Usage = func() {
		_, _ = fmt.Fprint(errOut, "Usage of entropy53:\n  entropy53 [--version] <command> [arguments]\n\nCommands:\n  chains    print the chains in the registry\n  round-at  print the first round at or after a time\n  get       print a verified round\n  latest    print the verified latest round\n\n")
		fs.PrintDefaults()
	}
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
		return 0
	}
	if fs.NArg() == 0 {
		writeError(errOut, "no command given.")
		fs.Usage()
		return 2
	}
	return dispatch(fs.Arg(0), fs.Args()[1:], out, errOut, now)
}

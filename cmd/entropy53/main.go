// Command entropy53 is the command-line client for drand rounds served over DNS.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/Crank-Git/entropy53/internal/version"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}

// run parses args and writes the result to out.
// It returns an error when the flags are invalid.
func run(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("entropy53", flag.ContinueOnError)
	showVersion := fs.Bool("version", false, "print the build version and exit")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *showVersion {
		_, err := fmt.Fprintln(out, version.String())
		return err
	}
	return nil
}

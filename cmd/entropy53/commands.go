package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Crank-Git/entropy53/chain"
)

// knownChains lists the registry in output order, because package chain exports no list.
var knownChains = []chain.Chain{chain.Quicknet, chain.Default, chain.Evmnet}

const usageExit = 2

// writeError writes an error line to errOut. The text is the whole message after "error: ".
func writeError(errOut io.Writer, msg string) {
	_, _ = fmt.Fprintf(errOut, "error: %s\n", msg)
}

// parseArgs parses flags and positional arguments in any order.
// The flag package stops at the first positional argument, and the spec writes flags after it.
// A help request returns flag.ErrHelp.
func parseArgs(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			return positional, nil
		}
		positional = append(positional, args[0])
		args = args[1:]
	}
}

// lookupChain returns the chain for a beacon ID.
// An unknown ID returns an error that names the known chains.
func lookupChain(id string) (chain.Chain, error) {
	c, err := chain.Lookup(id)
	if err != nil {
		names := make([]string, len(knownChains))
		for i, k := range knownChains {
			names[i] = k.BeaconID
		}
		return chain.Chain{}, fmt.Errorf("unknown chain %q. Known chains: %s", id, strings.Join(names, ", "))
	}
	return c, nil
}

// dispatch runs one command and returns its exit code.
// The clock is an argument so that a test can fix the time.
func dispatch(cmd string, args []string, out, errOut io.Writer, now func() time.Time) int {
	switch cmd {
	case "chains":
		return chains(args, out, errOut)
	case "round-at":
		return roundAt(args, out, errOut, now)
	default:
		writeError(errOut, fmt.Sprintf("unknown command %q.", cmd))
		return usageExit
	}
}

// newFlagSet builds the flag set of one command. Usage output goes to errOut.
func newFlagSet(cmd, synopsis string, errOut io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet("entropy53 "+cmd, flag.ContinueOnError)
	fs.SetOutput(errOut)
	fs.Usage = func() {
		_, _ = fmt.Fprintf(errOut, "Usage of %s:\n  %s\n", fs.Name(), synopsis)
		fs.PrintDefaults()
	}
	return fs
}

// parseCommand parses the arguments of one command and checks the positional count.
// It returns ok false with the exit code to return when parsing fails or the count is wrong.
func parseCommand(fs *flag.FlagSet, args []string, count int, errOut io.Writer) (pos []string, code int, ok bool) {
	pos, err := parseArgs(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil, 0, false
		}
		return nil, usageExit, false
	}
	if len(pos) != count {
		writeError(errOut, fmt.Sprintf("%s takes %d argument(s), and got %d.", fs.Name(), count, len(pos)))
		return nil, usageExit, false
	}
	return pos, 0, true
}

func chains(args []string, out, errOut io.Writer) int {
	fs := newFlagSet("chains", "entropy53 chains", errOut)
	if _, code, ok := parseCommand(fs, args, 0, errOut); !ok {
		return code
	}
	for _, c := range knownChains {
		_, _ = fmt.Fprintln(out, c.BeaconID)
	}
	return 0
}

func roundAt(args []string, out, errOut io.Writer, now func() time.Time) int {
	fs := newFlagSet("round-at", "entropy53 round-at <chain> <RFC 3339 time>", errOut)
	pos, code, ok := parseCommand(fs, args, 2, errOut)
	if !ok {
		return code
	}
	c, err := lookupChain(pos[0])
	if err != nil {
		writeError(errOut, err.Error()+".")
		return usageExit
	}
	// time.RFC3339 requires a zone offset, and a local time can change the round.
	t, err := time.Parse(time.RFC3339, pos[1])
	if err != nil {
		writeError(errOut, fmt.Sprintf("the time %q is not RFC 3339 with a zone offset.", pos[1]))
		return usageExit
	}
	n, err := c.RoundAfter(t)
	if err != nil {
		writeError(errOut, err.Error())
		return usageExit
	}
	_, _ = fmt.Fprintln(out, n)
	if t.Before(now()) {
		_, _ = fmt.Fprintf(errOut, "warning: round %d already exists. A draw must not use it.\n", n)
	}
	return 0
}

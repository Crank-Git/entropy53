package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/Crank-Git/entropy53/chain"
	"github.com/Crank-Git/entropy53/client"
)

// The exit codes of get and latest. Code 2 is usageExit.
const (
	exitNotPublished = 3
	exitWithheld     = 4
	exitInvalid      = 5
	exitStale        = 6
	exitTransport    = 7
)

// latestWarning goes to standard error before each latest query (FR-cli-11).
const latestWarning = "warning: latest is not for a draw. Use get with a pinned round number.\n"

// roundJSON is the --json form of a verified round.
// Error is set only for a stale round, which the CLI still prints.
type roundJSON struct {
	Chain             string `json:"chain"`
	Round             uint64 `json:"round"`
	Time              string `json:"time"`
	Randomness        string `json:"randomness"`
	Signature         string `json:"signature"`
	PreviousSignature string `json:"previous_signature"`
	Verified          bool   `json:"verified"`
	Error             string `json:"error,omitempty"`
}

// writeRound writes the verified round r of chain c to out.
// The time is UTC, so the output does not depend on the local zone.
// errText is added to the JSON object when it is not empty.
func writeRound(out io.Writer, c chain.Chain, r client.Round, asJSON bool, errText string) {
	rt := c.RoundTime(r.Number).UTC().Format(time.RFC3339)
	randomness := hex.EncodeToString(r.Randomness[:])
	signature := hex.EncodeToString(r.Signature)
	if asJSON {
		// Marshal cannot fail on a struct of strings, a number and a bool.
		b, _ := json.Marshal(roundJSON{
			Chain:             r.Chain,
			Round:             r.Number,
			Time:              rt,
			Randomness:        randomness,
			Signature:         signature,
			PreviousSignature: hex.EncodeToString(r.PreviousSignature),
			Verified:          true,
			Error:             errText,
		})
		_, _ = fmt.Fprintf(out, "%s\n", b)
		return
	}
	_, _ = fmt.Fprintf(out, "chain:      %s\nround:      %d\ntime:       %s\nrandomness: %s\nsignature:  %s\nverified:   yes\n",
		r.Chain, r.Number, rt, randomness, signature)
}

// fail reports an error and returns code.
// The text goes to errOut. With --json the object {"error":text} also goes to out.
func fail(out, errOut io.Writer, asJSON bool, code int, text string) int {
	writeError(errOut, text)
	if asJSON {
		b, _ := json.Marshal(struct {
			Error string `json:"error"`
		}{text})
		_, _ = fmt.Fprintf(out, "%s\n", b)
	}
	return code
}

// exitCode returns the exit code of a client error.
func exitCode(err error) int {
	switch {
	case errors.Is(err, client.ErrNotPublished):
		return exitNotPublished
	case errors.Is(err, client.ErrWithheld):
		return exitWithheld
	case errors.Is(err, client.ErrInvalid):
		return exitInvalid
	case errors.Is(err, client.ErrStale):
		return exitStale
	default:
		return exitTransport
	}
}

// errorText returns the message for a client error.
// n is the round number, or 0 for latest.
func errorText(err error, c chain.Chain, n uint64, r client.Round) string {
	switch {
	case errors.Is(err, client.ErrNotPublished):
		return fmt.Sprintf("round %d is not published. Its round time is %s.", n, c.RoundTime(n).UTC().Format(time.RFC3339))
	case errors.Is(err, client.ErrWithheld) && n == 0:
		return "the latest round is withheld."
	case errors.Is(err, client.ErrWithheld):
		return fmt.Sprintf("round %d is withheld. Its round time plus the grace time has passed.", n)
	case errors.Is(err, client.ErrInvalid):
		return "no record verifies. The gateway or the resolver returned a bad record."
	case errors.Is(err, client.ErrStale):
		return fmt.Sprintf("latest round %d is stale.", r.Number)
	default:
		return err.Error()
	}
}

func getCmd(args []string, out, errOut io.Writer, now func() time.Time) int {
	return readCmd("get", "entropy53 get <chain> <round> --zone <zone> [--resolver <host:port>] [--json]", 2, args, out, errOut, now)
}

func latestCmd(args []string, out, errOut io.Writer, now func() time.Time) int {
	return readCmd("latest", "entropy53 latest <chain> --zone <zone> [--resolver <host:port>] [--json]", 1, args, out, errOut, now)
}

// readCmd runs get (count 2) or latest (count 1).
func readCmd(name, synopsis string, count int, args []string, out, errOut io.Writer, now func() time.Time) int {
	fs := newFlagSet(name, synopsis, errOut)
	zone := fs.String("zone", "", "the zone of the gateway (required)")
	resolver := fs.String("resolver", "", "the resolver as host:port. The default is the system resolver")
	asJSON := fs.Bool("json", false, "print one JSON object")
	pos, code, ok := parseCommand(fs, args, count, errOut)
	if !ok {
		return code
	}
	usage := func(text string) int { return fail(out, errOut, *asJSON, usageExit, text) }
	if *zone == "" {
		return usage("--zone is required.")
	}
	c, err := lookupChain(pos[0])
	if err != nil {
		return usage(err.Error() + ".")
	}
	var n uint64
	if name == "get" {
		n, err = strconv.ParseUint(pos[1], 10, 64)
		if err != nil || n < 1 || n > chain.MaxRound {
			return usage(fmt.Sprintf("the round %q is not a number from 1 to %d.", pos[1], uint64(chain.MaxRound)))
		}
	}
	cl, err := client.New(client.Config{Zone: *zone, Chain: c, Resolver: *resolver, Clock: now})
	if err != nil {
		return usage(err.Error() + ".")
	}
	var r client.Round
	if name == "get" {
		r, err = cl.Round(context.Background(), n)
	} else {
		_, _ = fmt.Fprint(errOut, latestWarning)
		r, err = cl.Latest(context.Background())
	}
	if err == nil {
		writeRound(out, c, r, *asJSON, "")
		return 0
	}
	text := errorText(err, c, n, r)
	if errors.Is(err, client.ErrStale) {
		// Latest verified the round before it reported the stale state, so the CLI prints the round too.
		writeRound(out, c, r, *asJSON, text)
		writeError(errOut, text)
		return exitStale
	}
	return fail(out, errOut, *asJSON, exitCode(err), text)
}

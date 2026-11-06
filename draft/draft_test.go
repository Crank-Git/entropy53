package draft

import (
	"encoding/hex"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/Crank-Git/entropy53/chain"
)

// draftFile is the kramdown-rfc source. The tests read text only and need no Ruby or Python tool.
const draftFile = "draft-crank-drand-dns.md"

var (
	headingRE = regexp.MustCompile(`^# (.+?)(?: \{#[^}]*\})?\s*$`)
	// chainRE matches the first mention of a chain. The word "default" also occurs in plain English,
	// so the pattern accepts it only inside backticks or inside a name.
	chainRE = regexp.MustCompile("\\b(quicknet|evmnet)\\b|`default`|\\.default\\.")
	spaceRE = regexp.MustCompile(`\s+`)
)

// readDraft returns the draft source with every run of white space reduced to one space.
func readDraft(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(draftFile)
	if err != nil {
		t.Fatalf("read the draft: %v", err)
	}
	return string(raw)
}

func flatten(s string) string { return spaceRE.ReplaceAllString(s, " ") }

// sections returns the body of each top-level section, keyed by its title.
func sections(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	var title string
	var body strings.Builder
	flush := func() {
		if title != "" {
			out[title] = body.String()
		}
		body.Reset()
	}
	for _, line := range strings.Split(readDraft(t), "\n") {
		if m := headingRE.FindStringSubmatch(line); m != nil {
			flush()
			title = m[1]
			continue
		}
		body.WriteString(line + "\n")
	}
	flush()
	return out
}

func section(t *testing.T, title string) string {
	t.Helper()
	body, ok := sections(t)[title]
	if !ok {
		t.Fatalf("the draft has no section %q", title)
	}
	return body
}

// frontMatter returns the YAML block. The closing "---" line also opens the abstract, as in "--- abstract".
func frontMatter(t *testing.T) string {
	t.Helper()
	parts := strings.SplitN(readDraft(t), "\n---", 2)
	if len(parts) != 2 || !strings.HasPrefix(parts[0], "---\n") {
		t.Fatal("the draft has no front matter")
	}
	return parts[0]
}

func requireText(t *testing.T, text string, wants ...string) {
	t.Helper()
	flat := flatten(text)
	for _, w := range wants {
		if !strings.Contains(flat, w) {
			t.Errorf("the draft does not contain %q", w)
		}
	}
}

func TestDraftHasEachRequiredSection(t *testing.T) {
	for _, title := range []string{
		"Name Shapes",
		"Record Body",
		"Answer Rules",
		"Client Procedure",
		"Chain Parameters",
		"Security Considerations",
		"IANA Considerations",
		"Test Vectors",
	} {
		section(t, title)
	}
}

func TestDraftStatesInformationalStatus(t *testing.T) {
	if !regexp.MustCompile(`(?m)^category: info$`).MatchString(frontMatter(t)) {
		t.Error("the front matter does not state category: info")
	}
}

func TestDraftAuthorBlockHasNoOrganization(t *testing.T) {
	_, rest, ok := strings.Cut(frontMatter(t), "\nauthor:\n")
	if !ok {
		t.Fatal("the front matter has no author block")
	}
	// A reference entry below the author block names the drand organization, so the check stops at the next top-level key.
	block := regexp.MustCompile(`(?m)^\S.*`).Split(rest, 2)[0]
	requireText(t, block, "name: Christian Crank", "email: info@nocturnaltendenciesllc.com")
	if regexp.MustCompile(`(?m)^\s*-?\s*(org|organization|ins)\s*:`).MatchString(block) {
		t.Error("the author block names an organization")
	}
}

func TestDraftHasBCP14Boilerplate(t *testing.T) {
	requireText(t, readDraft(t), "{::boilerplate bcp14-tagged}")
}

func TestQuicknetIsFirstExampleInEachSection(t *testing.T) {
	for _, title := range []string{"Name Shapes", "Record Body", "Answer Rules", "Client Procedure", "Chain Parameters"} {
		m := chainRE.FindString(section(t, title))
		if m == "" {
			t.Errorf("section %q names no chain", title)
			continue
		}
		if m != "quicknet" {
			t.Errorf("section %q names %q before quicknet", title, m)
		}
	}
}

func TestDraftStatesLatestIsOptionalWithNoSecurityGain(t *testing.T) {
	requireText(t, section(t, "Answer Rules"),
		"A gateway MAY serve the latest name.",
		"The latest name gives no security gain.")
}

func TestDraftForbidsNXDOMAINForFutureRoundWithReason(t *testing.T) {
	requireText(t, section(t, "Answer Rules"),
		"MUST NOT return NXDOMAIN for a future round",
		"{{RFC8020}}",
		"will exist")
}

func TestDraftRequiresClientToComputeRandomnessAndVerify(t *testing.T) {
	requireText(t, section(t, "Client Procedure"),
		"MUST NOT return a value from the record, or act on one, until the signature verifies",
		"MUST compute `randomness`",
		"MUST NOT read `randomness` from a gateway")
}

func TestDraftStatesDefaultRoundOneException(t *testing.T) {
	requireText(t, section(t, "Record Body"),
		"Round 1 of `default` has a previous signature of 32 bytes",
		"gateway MUST return SERVFAIL",
		"it cannot verify, because the previous signature of that round has 32 bytes")
}

func TestDraftStatesBase64RejectionCases(t *testing.T) {
	requireText(t, section(t, "Record Body"),
		"a character outside the standard alphabet",
		"padding that is missing, too long, or not at the end",
		"non-zero bits")
}

func TestDraftStatesRecordBodyCheckOrder(t *testing.T) {
	requireText(t, section(t, "Record Body"),
		"The version check and the round number check come after the length check")
}

func TestDraftStatesNoIANAAction(t *testing.T) {
	requireText(t, section(t, "IANA Considerations"), "This document has no IANA actions.")
}

func TestDraftChainParametersEqualRegistry(t *testing.T) {
	body := section(t, "Chain Parameters")
	for _, id := range []string{"quicknet", "default", "evmnet"} {
		c, err := chain.Lookup(id)
		if err != nil {
			t.Fatal(err)
		}
		sub := subsection(t, body, id)
		for _, want := range []string{
			"Chain hash: " + c.ChainHash,
			"Scheme: " + c.SchemeID,
			"Public key: " + hex.EncodeToString(c.PublicKey),
			"Genesis: " + strconv.FormatInt(c.Genesis, 10),
			"Period: " + strconv.FormatInt(c.Period, 10) + " seconds",
			"Signature length: " + strconv.Itoa(c.SigLen) + " bytes",
			"Previous signature length: " + strconv.Itoa(c.PrevSigLen) + " bytes",
		} {
			if !strings.Contains(sub, want) {
				t.Errorf("chain %s: the draft does not state %q", id, want)
			}
		}
	}
}

// subsection returns the lines under the "## `id`" heading of body, up to the next "## " heading.
func subsection(t *testing.T, body, id string) string {
	t.Helper()
	_, rest, ok := strings.Cut(body, "\n## `"+id+"`")
	if !ok {
		t.Fatalf("the draft has no subsection for chain %s", id)
	}
	sub, _, _ := strings.Cut(rest, "\n## ")
	return sub
}

func TestDraftKeepsRetiredChainRule(t *testing.T) {
	requireText(t, section(t, "Chain Parameters"),
		"A retired chain stays in this document in a historical note.")
}

func TestDraftStatesFormatVersionRule(t *testing.T) {
	requireText(t, section(t, "Record Body"),
		"A change to the layout MUST change the version byte.",
		"The vectors of an earlier version stay as a separate vector set.")
}

// The security tests check that the text exists. A person checks that the text is true.

func TestSecuritySectionCoversWithheldRound(t *testing.T) {
	requireText(t, section(t, "Security Considerations"),
		"A gateway, a resolver or any party on the path can withhold a round.",
		`reports "withheld"`,
		"loss of availability")
}

func TestSecuritySectionCoversSelectionAmongLatestRounds(t *testing.T) {
	requireText(t, section(t, "Security Considerations"),
		"A gateway can select among recent rounds at a latest name.",
		"A pinned round has exactly one valid value",
		"A draw MUST use a pinned round and MUST NOT use a latest name.")
}

func TestSecuritySectionCoversSpoofedRecordInResolverCache(t *testing.T) {
	requireText(t, section(t, "Security Considerations"),
		"place a false record in the cache of a resolver",
		"A false record fails verification",
		"until its TTL ends",
		"The caller can select another resolver.")
}

func TestSecuritySectionCoversClientClock(t *testing.T) {
	requireText(t, section(t, "Security Considerations"),
		"The clock has no effect on whether a signature verifies.",
		"hide a stale latest round",
		"A client MUST NOT read a clock when it verifies a signature.")
}

func TestSecuritySectionCoversQueryPrivacy(t *testing.T) {
	requireText(t, section(t, "Security Considerations"),
		"The name of a query holds the chain and the round number.",
		"They learn which rounds a client reads",
		"This document gives no protection against that observation.")
}

func TestSecuritySectionCoversEachOtherThreatModelAttacker(t *testing.T) {
	requireText(t, section(t, "Security Considerations"),
		"A gateway can return a forged signature",
		"A valid round with another round number fails",
		"A gateway has no advantage over any other reader.",
		"junk never reaches a resolver cache through the gateway",
		"a gain of about 7.7",
		"A query flood for random old round numbers",
		"A query flood for random future round numbers costs arithmetic only",
		"all clients accept it")
}

func TestAppendixHoldsOneVectorPerChain(t *testing.T) {
	appendix := section(t, "Test Vectors")
	if got := strings.Count(appendix, "\n~~~ vector\n"); got != 3 {
		t.Errorf("the appendix holds %d vector blocks, want 3", got)
	}
	for _, id := range []string{"quicknet", "default", "evmnet"} {
		if got := strings.Count(appendix, "\nbeacon_id: "+id+"\n"); got != 1 {
			t.Errorf("the appendix holds %d vectors for chain %s, want 1", got, id)
		}
	}
}

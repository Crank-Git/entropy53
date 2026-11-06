package draft

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// vectorsFile is the vector file that the appendix must equal.
const vectorsFile = "../testdata/vectors.json"

// vector holds one vector with every value as text, so that a draft and the file compare field by field.
type vector struct {
	BeaconID          string
	Round             string
	Signature         string
	PreviousSignature string
	TXT               []string
	Randomness        string
}

type jsonVector struct {
	BeaconID          string   `json:"beacon_id"`
	Round             uint64   `json:"round"`
	Signature         string   `json:"signature"`
	PreviousSignature string   `json:"previous_signature"`
	TXT               []string `json:"txt"`
	Randomness        string   `json:"randomness"`
}

// field returns the value that a key sets, or nil for an unknown key. A txt key addresses the last string.
func (v *vector) field(key string) *string {
	switch key {
	case "beacon_id":
		return &v.BeaconID
	case "round":
		return &v.Round
	case "signature":
		return &v.Signature
	case "previous_signature":
		return &v.PreviousSignature
	case "randomness":
		return &v.Randomness
	case "txt":
		return &v.TXT[len(v.TXT)-1]
	}
	return nil
}

// parseAppendix returns the vectors of the appendix text.
// A block opens with a "~~~ vector" line and closes with a "~~~" line. A key line starts at column 0.
// A line that starts with white space continues the value above it, with no separator.
func parseAppendix(appendix string) ([]vector, error) {
	var out []vector
	var cur *vector
	var last *string // the value that a continuation line extends
	for n, line := range strings.Split(appendix, "\n") {
		where := fmt.Sprintf("appendix line %d", n+1)
		switch {
		case cur == nil && strings.TrimRight(line, " ") == "~~~ vector":
			cur, last = &vector{}, nil
		case cur == nil:
			continue
		case line == "~~~":
			out, cur = append(out, *cur), nil
		case strings.TrimSpace(line) == "":
			return nil, fmt.Errorf("%s: a blank line inside a vector block", where)
		case line[0] == ' ' || line[0] == '\t':
			if last == nil {
				return nil, fmt.Errorf("%s: a continuation line with no value above it", where)
			}
			*last += strings.TrimSpace(line)
		default:
			key, value, ok := strings.Cut(line, ":")
			if !ok {
				return nil, fmt.Errorf("%s: no colon in %q", where, line)
			}
			if key == "txt" {
				cur.TXT = append(cur.TXT, "")
			}
			if last = cur.field(key); last == nil {
				return nil, fmt.Errorf("%s: unknown key %q", where, key)
			}
			*last = strings.TrimSpace(value)
		}
	}
	if cur != nil {
		return nil, fmt.Errorf("the last vector block has no closing fence")
	}
	return out, nil
}

func readFileVectors(t *testing.T) []vector {
	t.Helper()
	raw, err := os.ReadFile(vectorsFile)
	if err != nil {
		t.Fatalf("read the vector file: %v", err)
	}
	var doc struct {
		Vectors []jsonVector `json:"vectors"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse the vector file: %v", err)
	}
	out := make([]vector, len(doc.Vectors))
	for i, v := range doc.Vectors {
		out[i] = vector{v.BeaconID, strconv.FormatUint(v.Round, 10), v.Signature, v.PreviousSignature, v.TXT, v.Randomness}
	}
	return out
}

// compareVectors returns one message for each difference between the draft and the file.
// Each side must hold the same set of beacon IDs.
func compareVectors(draft, file []vector) []string {
	var diffs []string
	byID := func(side string, vs []vector) map[string]vector {
		m := map[string]vector{}
		for _, v := range vs {
			if _, dup := m[v.BeaconID]; dup {
				diffs = append(diffs, fmt.Sprintf("%s holds more than one vector for %q", side, v.BeaconID))
			}
			m[v.BeaconID] = v
		}
		return m
	}
	d, f := byID("the draft", draft), byID("the file", file)
	for id := range f {
		if _, ok := d[id]; !ok {
			diffs = append(diffs, fmt.Sprintf("the draft has no vector for %q", id))
		}
	}
	for id, dv := range d {
		fv, ok := f[id]
		if !ok {
			diffs = append(diffs, fmt.Sprintf("the file has no vector for %q", id))
			continue
		}
		for _, field := range []struct{ name, d, f string }{
			{"round", dv.Round, fv.Round},
			{"signature", dv.Signature, fv.Signature},
			{"previous_signature", dv.PreviousSignature, fv.PreviousSignature},
			{"randomness", dv.Randomness, fv.Randomness},
		} {
			if field.d != field.f {
				diffs = append(diffs, fmt.Sprintf("%s: %s differs: draft %q, file %q", id, field.name, field.d, field.f))
			}
		}
		if !slices.Equal(dv.TXT, fv.TXT) {
			diffs = append(diffs, fmt.Sprintf("%s: txt differs: draft %q, file %q", id, dv.TXT, fv.TXT))
		}
	}
	slices.Sort(diffs)
	return diffs
}

func draftVectors(t *testing.T) []vector {
	t.Helper()
	vs, err := parseAppendix(section(t, "Test Vectors"))
	if err != nil {
		t.Fatalf("parse the appendix: %v", err)
	}
	return vs
}

func TestDraftVectorsEqualFile(t *testing.T) {
	file := readFileVectors(t)
	if len(file) == 0 {
		t.Fatal("the vector file holds no vector")
	}
	for _, d := range compareVectors(draftVectors(t), file) {
		t.Error(d)
	}
}

// cloneVector copies the slice so that a change to one side leaves the other side alone.
func cloneVector(v vector) vector {
	v.TXT = slices.Clone(v.TXT)
	return v
}

func TestChangedByteInEachVectorFieldFailsComparison(t *testing.T) {
	file := readFileVectors(t)
	flip := func(s string) string {
		if s == "" {
			return "0"
		}
		b := []byte(s)
		b[len(b)/2] ^= 1
		return string(b)
	}
	mutations := map[string]func(*vector){
		"beacon_id":          func(v *vector) { v.BeaconID = flip(v.BeaconID) },
		"round":              func(v *vector) { v.Round = flip(v.Round) },
		"signature":          func(v *vector) { v.Signature = flip(v.Signature) },
		"previous_signature": func(v *vector) { v.PreviousSignature = flip(v.PreviousSignature) },
		"txt":                func(v *vector) { v.TXT[0] = flip(v.TXT[0]) },
		"randomness":         func(v *vector) { v.Randomness = flip(v.Randomness) },
	}
	for i, base := range file {
		for field, mutate := range mutations {
			t.Run(base.BeaconID+"/"+field, func(t *testing.T) {
				changed := make([]vector, len(file))
				for j, v := range file {
					changed[j] = cloneVector(v)
				}
				mutate(&changed[i])
				if len(compareVectors(draftVectors(t), changed)) == 0 {
					t.Errorf("a changed byte in %s of %s passes the comparison", field, base.BeaconID)
				}
			})
		}
	}
}

func TestMissingDraftVectorFailsComparison(t *testing.T) {
	file := readFileVectors(t)
	if len(compareVectors(file[1:], file)) == 0 {
		t.Error("a draft with a missing vector passes the comparison")
	}
}

func TestExtraDraftVectorFailsComparison(t *testing.T) {
	file := readFileVectors(t)
	extra := cloneVector(file[0])
	extra.BeaconID = "extra"
	if len(compareVectors(append(slices.Clone(file), extra), file)) == 0 {
		t.Error("a draft with an extra vector passes the comparison")
	}
}

func TestFoldedValueEqualsUnfoldedValue(t *testing.T) {
	folded := "~~~ vector\nbeacon_id: x\nround: 1\nsignature: ab\n  cd\n\tef\nprevious_signature:\ntxt: AA\n  BB\ntxt: CC\nrandomness: 00\n~~~\n"
	vs, err := parseAppendix(folded)
	if err != nil {
		t.Fatal(err)
	}
	want := vector{"x", "1", "abcdef", "", []string{"AABB", "CC"}, "00"}
	if len(vs) != 1 || len(compareVectors(vs, []vector{want})) != 0 {
		t.Errorf("parsed %+v, want %+v", vs, want)
	}
}

func TestMalformedAppendixBlockFailsParse(t *testing.T) {
	for name, text := range map[string]string{
		"unknown key":         "~~~ vector\nbeacon_id: x\nbogus: 1\n~~~\n",
		"line with no colon":  "~~~ vector\nbeacon_id x\n~~~\n",
		"orphan continuation": "~~~ vector\n  ab\n~~~\n",
		"blank line in block": "~~~ vector\nbeacon_id: x\n\nround: 1\n~~~\n",
		"unclosed fence":      "~~~ vector\nbeacon_id: x\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseAppendix(text); err == nil {
				t.Error("parse accepted a malformed block")
			}
		})
	}
}

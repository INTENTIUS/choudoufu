// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// This file is the leg that makes internal/live/check's identity golden
// non-silenceable.
//
// TestIdentityGolden pins 1320 rendered identities. It is the only instrument
// in this repository that measures the value a marker will carry rather than
// whether something refused - every other one counts refusals, and a marker
// can be wrong without anything refusing. Six defects shipped green through
// that gap.
//
// But the golden regenerates with -update, which is one word, and the diff it
// produces is a changed testdata file among 1331 lines. Nobody reads that as
// an alarm. So the rule "explain a moved line, do not silence it" lived only
// in prose, in two files, and prose is what this project keeps discovering
// was stale.
//
// The shape is therefore pinned again here, in Go, where moving it is a
// one-line diff in a file named for the purpose. -update alone no longer
// makes the tree green: it makes THIS test fail, and the only way past is to
// edit a number next to a comment asking why.
//
// Why the shape and not the whole file: re-pinning 1331 lines here would just
// be the golden twice. The shape is what a silenced regression moves. The
// validation case is on record - reverting #251's conversion fabricated three
// identities and lost two correct ones, which is CONCRETE 658 -> 659. An
// exact pin catches that. A floor does not, and neither does any aggregate
// this repository was recording at the time: the instance count went UP.

// identityGoldenPin is the shape of internal/live/check/testdata/identity-golden.txt.
//
// TO CHANGE A NUMBER HERE, say why in the commit message, naming what moved
// and in which direction. A rising CONCRETE is usually the campaign working.
// A falling one, or a NEEDS_DISCOVERY that became CONCRETE in a fixture
// nobody touched, is the shape of a fabricated identity - which is worse than
// a refusal, because it is a marker this tool writes into a real cloud tag.
//
// Recompute with:
//
//	env -u PWD go test ./internal/live/check -run TestIdentityGolden -update
//
// then read the "# shape:" block at the top of the regenerated file.
var identityGoldenPin = map[string]int{
	"CONCRETE":        672,
	"NEEDS_DISCOVERY": 551,
	"PARENT_DERIVED":  95,
	"RECORD_BACKED":   17,
}

const (
	identityGoldenPinInstances = 1335
	identityGoldenPinDirs      = 382

	// identityGoldenSweepFloor is the anti-tamper leg, in the same spirit as
	// universeFloor in admission_coverage_test.go.
	//
	// Every pin above can be satisfied by making the sweep smaller: drop a
	// root, tighten the file filter, and the numbers fall to something you
	// then re-pin, with each individual edit looking reasonable. This is the
	// number that must never go down. It is deliberately well below the
	// current 382 so that removing fixtures is not an event, and far enough
	// above zero that narrowing the walk to nothing is.
	identityGoldenSweepFloor = 300
)

// TestIdentityGoldenShapeIsPinned fails in both directions.
//
// Forwards: the golden's shape moved and nobody recorded why, which is what a
// bare -update produces.
//
// Backwards: this pin names a class the golden no longer emits, or the header
// disagrees with a recount of the body beneath it. Both mean the pin has
// stopped describing the thing it pins, and a pin that describes nothing
// passes forever.
func TestIdentityGoldenShapeIsPinned(t *testing.T) {
	path := filepath.Join(repoRoot(t), "internal", "live", "check", "testdata", "identity-golden.txt")

	header, bodyCounts, bodyInstances := readIdentityGolden(t, path)

	// The header is written by the same walk that writes the body, so these
	// agreeing proves only that the file was not hand-edited afterwards -
	// which is exactly the edit this test would otherwise miss, since the
	// header is where a reader looks and the body is where the truth is.
	if got, want := header["instances"], bodyInstances; got != want {
		t.Errorf("header says instances=%d, body holds %d rows: the header was edited without regenerating", got, want)
	}
	for class, n := range bodyCounts {
		if got := header["class "+class]; got != n {
			t.Errorf("header says class %s=%d, body holds %d: the header was edited without regenerating", class, got, n)
		}
	}

	if got := header["dirs"]; got < identityGoldenSweepFloor {
		t.Errorf("the golden swept %d configuration directories, below the floor of %d.\n"+
			"Every count in identityGoldenPin can be satisfied by sweeping less, so this is the leg that is not allowed to move.\n"+
			"If the tree genuinely shrank, lower the floor in its own commit that says so - not in the commit that shrank it.",
			got, identityGoldenSweepFloor)
	}
	if got := header["dirs"]; got != identityGoldenPinDirs {
		t.Errorf("the golden sweeps %d configuration directories, pinned at %d.\n"+
			"Adding fixtures moves this and is fine; say so and re-pin.",
			got, identityGoldenPinDirs)
	}
	if bodyInstances != identityGoldenPinInstances {
		t.Errorf("the golden holds %d instances, pinned at %d.\n"+
			"An instance is a marker this tool writes into a cloud tag. Rising is usually the campaign working; falling means something that resolved no longer does.\n"+
			"Neither is settled by re-running -update.",
			bodyInstances, identityGoldenPinInstances)
	}

	for _, class := range sortedKeys(identityGoldenPin) {
		want := identityGoldenPin[class]
		got, present := bodyCounts[class]
		if !present {
			t.Errorf("identityGoldenPin pins class %s at %d, but the golden no longer emits that class at all.\n"+
				"A pin on something that does not exist passes forever. Remove it, or find out why the class vanished.",
				class, want)
			continue
		}
		if got != want {
			t.Errorf("class %s: golden has %d, pinned at %d.\n"+
				"%s", class, got, want, identityGoldenClassAdvice(class, got, want))
		}
	}
	for _, class := range sortedKeys(bodyCounts) {
		if _, pinned := identityGoldenPin[class]; !pinned {
			t.Errorf("the golden emits class %s (%d instances) and identityGoldenPin does not mention it.\n"+
				"A new class is a new way for a marker to be rendered, and it is unpinned until it is listed here.",
				class, bodyCounts[class])
		}
	}
}

// identityGoldenClassAdvice says which direction of movement is the alarming
// one, per class, because they are not symmetric.
func identityGoldenClassAdvice(class string, got, want int) string {
	switch class {
	case "CONCRETE":
		if got > want {
			return "CONCRETE rose. That is usually the campaign working - but it is also exactly what a fabricated identity looks like,\n" +
				"and a fabricated marker is worse than a refusal. Read the added lines' rendered values before re-pinning."
		}
		return "CONCRETE fell. Something that rendered a real identity no longer does, which is a regression unless a fixture was deleted."
	case "NEEDS_DISCOVERY":
		if got < want {
			return "NEEDS_DISCOVERY fell. If CONCRETE rose by the same amount, resources became identifiable offline, which is the goal.\n" +
				"If it fell on its own, instances went missing."
		}
		return "NEEDS_DISCOVERY rose. Something that used to resolve offline now defers to a live read."
	default:
		return "Read the diff and say which fixtures moved."
	}
}

// readIdentityGolden parses the "# shape:" header and independently recounts
// the body, so the two can be compared against each other.
func readIdentityGolden(t *testing.T, path string) (header map[string]int, bodyCounts map[string]int, bodyInstances int) {
	t.Helper()

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("reading the identity golden: %s\n"+
			"This pin exists to guard that file; with the file absent it guards nothing, so this is a failure rather than a skip.", err)
	}
	defer f.Close()

	header = map[string]int{}
	bodyCounts = map[string]int{}

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "#") {
			for _, kv := range parseShapeLine(line) {
				header[kv.key] = kv.n
			}
			continue
		}
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 3 {
			t.Fatalf("malformed golden row (%d tab-separated fields, want at least 3): %q", len(fields), line)
		}
		bodyCounts[fields[2]]++
		bodyInstances++
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scanning the identity golden: %s", err)
	}
	if len(header) == 0 {
		t.Fatal("the identity golden carries no \"# shape:\" header.\n" +
			"That block is what makes a silenced regression visible in the first fifteen lines of a diff; regenerate with -update.")
	}
	return header, bodyCounts, bodyInstances
}

type shapeKV struct {
	key string
	n   int
}

// parseShapeLine reads "# shape: dirs=375 instances=1320" and
// "# shape: class CONCRETE=658".
func parseShapeLine(line string) []shapeKV {
	const marker = "# shape:"
	if !strings.HasPrefix(line, marker) {
		return nil
	}
	rest := strings.TrimSpace(strings.TrimPrefix(line, marker))

	var prefix string
	if after, ok := strings.CutPrefix(rest, "class "); ok {
		prefix, rest = "class ", after
	}

	var out []shapeKV
	for _, field := range strings.Fields(rest) {
		key, val, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		n, err := strconv.Atoi(val)
		if err != nil {
			continue
		}
		out = append(out, shapeKV{key: prefix + key, n: n})
	}
	return out
}

func sortedKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

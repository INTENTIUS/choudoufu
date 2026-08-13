// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"fmt"
	"math/rand"
	"regexp"
	"strings"
	"testing"
)

// The adversarial audit attacked discovery directly and could not break the
// behaviours below. That result lived in a scratch file that has since been
// cleaned, so all that survived of it was the audit's list of behaviours
// that held under attack. A property nobody
// pinned is a property nobody will notice losing, and these are the ones that
// decide whether a plan attaches itself to the right cloud object.
//
// Everything here is an abuse input: markers written by a hand that did not
// read the spec, or by two runs racing, or by somebody trying to make the
// tool confuse one resource for another. The existing suite covers the
// two-resource collision and the cross-type marker; these are the shapes it
// does not reach.

// ---------------------------------------------------------------------------
// Ambiguity: two or more live resources claiming one address
// ---------------------------------------------------------------------------

// TestCollisionNamesEveryClaimant. Two claimants is the case
// TestDiscoverCollision covers. Three is where a refusal written to report
// "the other one" quietly stops being complete, and the concurrency taxonomy
// on the docs page makes a specific promise about this: the plan "names BOTH
// live IDs and refuses to guess". With four racing applies it has to name all
// four, or an operator deletes one duplicate and is told about the next.
func TestCollisionNamesEveryClaimant(t *testing.T) {
	cloud := newFakeCloud()
	for _, id := range []string{"vpc-d", "vpc-a", "vpc-c", "vpc-b"} {
		cloud.own("aws_vpc", id, `aws_vpc.main`)
	}

	res, diags := discoverFixture(t, cloud, Request{})
	if !diags.HasErrors() {
		t.Fatalf("four resources claiming one address produced no error:\n%s", res)
	}

	problems := res.ProblemsOfKind(ProblemCollision)
	if len(problems) != 1 {
		t.Fatalf("want one collision problem covering all four, got %d:\n%s", len(problems), res)
	}
	p := problems[0]
	if got := strings.Join(p.LiveIDs, ","); got != "vpc-a,vpc-b,vpc-c,vpc-d" {
		t.Errorf("the collision names %q; it must name every claimant, in a stable order", got)
	}

	// And every one of them is in the message an operator actually reads,
	// not only in the structured problem.
	rendered := renderDiags(diags)
	for _, id := range []string{"vpc-a", "vpc-b", "vpc-c", "vpc-d"} {
		if !strings.Contains(rendered, id) {
			t.Errorf("the diagnostic does not name %s:\n%s", id, rendered)
		}
	}

	// Refusing to guess means refusing to bind, and refusing to reclassify.
	// A collided address that quietly became an orphan would be a deletion
	// candidate, which is the worst available answer to "I am not sure".
	if _, bound := res.BindingFor(mustAddr(t, `aws_vpc.main`)); bound {
		t.Error("a collided address was bound anyway")
	}
	for _, o := range res.Orphans {
		if o.Marker == `aws_vpc.main` {
			t.Errorf("a collided address became an orphan, and therefore a deletion candidate: %+v", o)
		}
	}
}

// TestCollisionDoesNotPoisonItsNeighbours. A collision is a fact about one
// address. The blast radius has to be that address: a run where one block was
// hand-edited badly must still bind everything else, or the refusal that was
// supposed to be conservative has instead made the whole estate unreadable -
// and an operator staring at "nothing bound" reaches for the state file.
func TestCollisionDoesNotPoisonItsNeighbours(t *testing.T) {
	cloud := newFakeCloud()
	cloud.own("aws_vpc", "vpc-1", `aws_vpc.main`)
	cloud.own("aws_vpc", "vpc-2", `aws_vpc.main`)
	cloud.own("aws_security_group", "sg-ok", `aws_security_group.main`)
	cloud.own("aws_subnet", "subnet-ok", `aws_subnet.this:a`)

	res, _ := discoverFixture(t, cloud, Request{})

	for _, addr := range []string{`aws_security_group.main`, `aws_subnet.this["a"]`} {
		if _, bound := res.BindingFor(mustAddr(t, addr)); !bound {
			t.Errorf("%s did not bind, though the collision was on an unrelated address:\n%s", addr, res)
		}
	}
	if _, bound := res.BindingFor(mustAddr(t, `aws_vpc.main`)); bound {
		t.Error("the collided address bound anyway")
	}
}

// TestCollisionOnAKeyedInstance. A for_each key is part of the address, so
// two resources claiming aws_subnet.this["a"] is the same failure as two
// claiming a plain address - and the escaped marker form (colon-separated) is
// where a comparison could quietly go wrong.
func TestCollisionOnAKeyedInstance(t *testing.T) {
	cloud := newFakeCloud()
	cloud.own("aws_subnet", "subnet-1", `aws_subnet.this:a`)
	cloud.own("aws_subnet", "subnet-2", `aws_subnet.this:a`)
	cloud.own("aws_subnet", "subnet-b", `aws_subnet.this:b`)

	res, diags := discoverFixture(t, cloud, Request{})
	if !diags.HasErrors() {
		t.Fatalf("two resources claiming one keyed instance produced no error:\n%s", res)
	}
	problems := res.ProblemsOfKind(ProblemCollision)
	if len(problems) != 1 {
		t.Fatalf("want one collision, got %d:\n%s", len(problems), res)
	}
	if got := problems[0].Addr.String(); got != `aws_subnet.this["a"]` {
		t.Errorf("the collision names %s, want the keyed instance", got)
	}
	// The sibling key is untouched: a bad "a" says nothing about "b".
	if _, bound := res.BindingFor(mustAddr(t, `aws_subnet.this["b"]`)); !bound {
		t.Errorf("the sibling key did not bind:\n%s", res)
	}
}

// ---------------------------------------------------------------------------
// Marker values chosen to confuse the reader
// ---------------------------------------------------------------------------

// TestHostileMarkerValuesAreMalformedAndNothingElse. floci accepts tag values
// real AWS rejects - `[`, `]`, `"` among them (chant/test/floci-gaps.md #7) -
// so an emulator, another cloud, or a hand-written tag can put shapes into a
// tofu-address that the escaping grammar never produces. Every one of them
// has to land in the malformed bucket: not bound to something that looks
// similar, not silently ignored, and above all not an orphan, because an
// orphan is a deletion candidate.
//
// The list is deliberately made of near-misses. A value that is obviously
// rubbish is easy; the dangerous ones are the values one character away from
// a real address.
//
// "module path" used to be one of these entries, pinning that
// "module.net.aws_vpc.main" was malformed. It is not, since 59b: a
// module-qualified address is exactly as well-formed as a root one, and
// this fixture's config declaring no "net" module makes the live resource
// an ordinary (non-actionable, since nothing declares it) unclaimed
// resource rather than a malformed marker. "provider prefix" below is the
// near-miss that stays malformed: a two-segment prefix in the same
// position as a module step, but whose first segment is not literally
// "module".
func TestHostileMarkerValuesAreMalformedAndNothingElse(t *testing.T) {
	for name, value := range map[string]string{
		"trailing separator":  `aws_vpc.main:`,
		"leading separator":   `:aws_vpc.main`,
		"double separator":    `aws_subnet.this::a`,
		"embedded newline":    "aws_vpc.main\naws_vpc.other",
		"embedded space":      `aws_vpc.main aws_vpc.other`,
		"provider prefix":     `aws.default.aws_vpc.main`,
		"data address":        `data.aws_vpc.main`,
		"whitespace padded":   ` aws_vpc.main `,
		"type only":           `aws_vpc`,
		"three segments":      `aws_vpc.main.extra`,
		"empty name":          `aws_vpc.`,
		"json fragment":       `{"type":"aws_vpc","name":"main"}`,
		"shell interpolation": `$(aws_vpc.main)`,
		"overlong":            "aws_vpc." + strings.Repeat("x", 300),
	} {
		t.Run(name, func(t *testing.T) {
			cloud := newFakeCloud()
			cloud.obj("aws_vpc", "vpc-hostile", map[string]string{
				TagEstate:  estateName,
				TagAddress: value,
			})

			res, diags := discoverFixture(t, cloud, Request{})
			if !diags.HasErrors() {
				t.Fatalf("the marker value %q produced no error:\n%s", value, res)
			}
			if len(res.ProblemsOfKind(ProblemMalformedMarker)) != 1 {
				t.Fatalf("the marker value %q is not reported as malformed:\n%s", value, res)
			}
			if len(res.Bindings) != 0 {
				t.Errorf("the marker value %q bound to something:\n%s", value, res)
			}
			if len(res.Unclaimed) != 0 {
				t.Errorf("the marker value %q was also collected as unclaimed, so it is both ours and nobody's:\n%s", value, res)
			}
			// An orphan is allowed here, and one shape of it is the right
			// answer: the resource IS this estate's, so saying nothing about
			// it would be the C4 hole. What it must never be is an
			// ACTIONABLE orphan - a removal candidate - because a marker
			// nobody can read is not an instruction to delete anything.
			for _, o := range res.Orphans {
				if o.Removal {
					t.Errorf("the marker value %q produced a removal candidate; an unreadable marker must never authorize a destroy: %s", value, o)
				}
				if o.Withheld == "" {
					t.Errorf("the marker value %q produced an orphan with no reason given for withholding it: %s", value, o)
				}
			}
		})
	}
}

// TestNormalizedMarkerValuesAreNotMalformed is the other side of the same
// coin, and the reason the list above does not contain the two values one
// would reach for first. A tag carrying an UNESCAPED address -
// aws_subnet.this["a"], written by a human copying it out of a plan - is not
// hostile input at all: EscapeAddress is idempotent, so discovery normalizes
// the observed value with the same function it applies to a declared address
// and the two meet in the middle. Refusing it would turn the most likely
// honest mistake into a named error, and the escaping rule exists precisely
// so that it does not have to be.
func TestNormalizedMarkerValuesAreNotMalformed(t *testing.T) {
	cloud := newFakeCloud()
	cloud.obj("aws_subnet", "subnet-a", map[string]string{
		TagEstate:  estateName,
		TagAddress: `aws_subnet.this["a"]`, // as a plan would print it
	})
	cloud.obj("aws_subnet", "subnet-b", map[string]string{
		TagEstate:  estateName,
		TagAddress: `aws_subnet.this:b`, // as the spec escapes it
	})

	res, diags := discoverFixture(t, cloud, Request{})
	if diags.HasErrors() {
		t.Fatalf("an unescaped-but-recognizable marker produced an error:\n%s", renderDiags(diags))
	}
	for addr, want := range map[string]string{
		`aws_subnet.this["a"]`: "subnet-a",
		`aws_subnet.this["b"]`: "subnet-b",
	} {
		b, bound := res.BindingFor(mustAddr(t, addr))
		if !bound {
			t.Errorf("%s did not bind:\n%s", addr, res)
			continue
		}
		if b.ImportID != want {
			t.Errorf("%s bound to %q, want %q", addr, b.ImportID, want)
		}
	}
}

// TestEstateNameIsMatchedWholeNotByPrefix. Two estates whose names share a
// prefix - "prod" and "prod-eu", the shape any team gets within a week of
// having two of anything - must not see each other's resources. A prefix or
// substring comparison anywhere in the ownership check would make one estate
// able to bind, rename, and ultimately destroy the other's resources, and it
// would do it silently because both markers are perfectly well-formed.
func TestEstateNameIsMatchedWholeNotByPrefix(t *testing.T) {
	for _, other := range []string{
		estateName + "-eu",
		estateName + "2",
		"pre-" + estateName,
		strings.ToUpper(estateName),
		estateName + " ",
	} {
		t.Run(other, func(t *testing.T) {
			cloud := newFakeCloud()
			cloud.obj("aws_vpc", "vpc-someone-elses", map[string]string{
				TagEstate:  other,
				TagAddress: `aws_vpc.main`,
			})

			res, diags := discoverFixture(t, cloud, Request{})
			if diags.HasErrors() {
				t.Fatalf("another estate's resource produced an error, which is not this estate's business:\n%s", renderDiags(diags))
			}
			if _, bound := res.BindingFor(mustAddr(t, `aws_vpc.main`)); bound {
				t.Errorf("estate %q bound a resource marked for estate %q", estateName, other)
			}
			for _, o := range res.Orphans {
				t.Errorf("a resource belonging to estate %q became this estate's orphan, and therefore a deletion candidate: %+v", other, o)
			}
			if len(res.ProblemsOfKind(ProblemMalformedMarker)) != 0 {
				t.Errorf("another estate's well-formed marker was called malformed:\n%s", res)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// The escaping grammar, as a pair of properties
// ---------------------------------------------------------------------------

// TestMarkerRoundTripsInTheDirectionThatMatters. The escaping rule is lossy,
// deliberately and in a documented way, so "it round-trips" needs saying
// precisely or it is not a property at all. The direction that has to hold
// absolutely is escape(unescape(m)) == m for every valid marker m: removal
// planning unescapes a marker to get an address to print, and if escaping
// that address back produced a different string then the destroy would be
// labelled with an address whose marker nothing would ever find again.
//
// The other direction, unescape(escape(a)) == a, holds for every address
// EXCEPT one shape, and that exception is the digit-key case the audit
// checked by hand: aws_eip.pool[0] and aws_eip.pool["0"] escape to the same
// marker, and it decodes as the count index. See UnescapeAddress's own doc
// comment for why the reading cannot mislead anything -
// TestDigitKeyAmbiguityCannotMisbind below is that argument as a test rather
// than as prose.
func TestMarkerRoundTripsInTheDirectionThatMatters(t *testing.T) {
	var addresses []string
	for _, typeName := range []string{"aws_vpc", "aws_subnet", "aws_eip", "aws_ssm_parameter"} {
		for _, name := range []string{"main", "this", "pool", "a", "x_y", "n0"} {
			addresses = append(addresses, fmt.Sprintf("%s.%s", typeName, name))
			for _, idx := range []int{0, 1, 9, 10, 100, 4294967295} {
				addresses = append(addresses, fmt.Sprintf("%s.%s[%d]", typeName, name, idx))
			}
			for _, key := range []string{"a", "b", "0", "1", "00", "007", "10", "a-b", "a_b", "A", "z9", "eu-west-1a"} {
				addresses = append(addresses, fmt.Sprintf("%s.%s[%q]", typeName, name, key))
			}
		}
	}

	digitKey := regexp.MustCompile(`\["[0-9]+"\]$`)
	markers := map[string][]string{}
	for _, addr := range addresses {
		escaped := EscapeAddress(addr)

		if !ValidMarkerAddress(escaped) {
			t.Errorf("%s escapes to %q, which the marker grammar rejects", addr, escaped)
			continue
		}
		markers[escaped] = append(markers[escaped], addr)

		back, ok := UnescapeAddress(escaped)
		if !ok {
			t.Errorf("%s escapes to %q, which does not unescape at all", addr, escaped)
			continue
		}

		// The direction that must hold for everything.
		if again := EscapeAddress(back.String()); again != escaped {
			t.Errorf("marker %q unescapes to %s, which escapes back to %q - removal would label a destroy with an address whose marker is a different string",
				escaped, back, again)
		}

		// The other direction, with its one documented exception.
		if got := back.String(); got != addr && !digitKey.MatchString(addr) {
			t.Errorf("%s escapes to %q and comes back as %s, and it is not the digit-key case", addr, escaped, got)
		}
	}

	// Every marker that two addresses share must be exactly the digit-key
	// pair: an index and the same digits as a string key. Anything else
	// sharing a marker would be a real ambiguity nobody has argued is safe.
	for escaped, sharing := range markers {
		if len(sharing) < 2 {
			continue
		}
		if len(sharing) != 2 {
			t.Errorf("%d addresses share the marker %q: %v", len(sharing), escaped, sharing)
			continue
		}
		a, b := sharing[0], sharing[1]
		if strings.ReplaceAll(a, `"`, "") != strings.ReplaceAll(b, `"`, "") {
			t.Errorf("%s and %s share the marker %q and are not the count/string-key pair", a, b, escaped)
		}
	}
	t.Logf("%d addresses over %d markers; %d markers shared by a count index and its digit string key",
		len(addresses), len(markers), len(addresses)-len(markers))
}

// TestDigitKeyAmbiguityCannotMisbind is UnescapeAddress's own safety argument,
// checked rather than believed: a live resource whose declared instance really
// is the digit STRING key binds during the scan and never reaches unescape at
// all, because the comparison discovery makes is between two escaped values
// and those two are the same string. The lossy decode is only ever reached by
// a marker no declared instance matched.
func TestDigitKeyAmbiguityCannotMisbind(t *testing.T) {
	// The estate fixture keys its subnets by name, so this uses a fake cloud
	// against a config whose for_each keys are digits: the marker is
	// "aws_subnet.this:0", which unescapes to aws_subnet.this[0] - an address
	// the configuration does not have.
	cloud := newFakeCloud()
	cloud.own("aws_subnet", "subnet-digit", `aws_subnet.this:0`)

	res, _ := discoverFixture(t, cloud, Request{})

	// Against THIS fixture (keys "a" and "b") the marker matches nothing, and
	// the important half is what happens then: it is reported, and it is not
	// silently attached to aws_subnet.this["a"] or to a count index of a
	// block that has no count.
	if _, bound := res.BindingFor(mustAddr(t, `aws_subnet.this["a"]`)); bound {
		t.Error("a digit-keyed marker bound to a differently-keyed declared instance")
	}
	for _, o := range res.Orphans {
		if o.Removal {
			t.Errorf("a digit-keyed marker matching no declared instance became a removal candidate: %s", o)
		}
	}
	// And the decode itself is the documented one, so the label a plan would
	// print is the count reading rather than a guess at a string key.
	addr, ok := UnescapeAddress(`aws_subnet.this:0`)
	if !ok {
		t.Fatal("the digit-keyed marker does not unescape")
	}
	if addr.String() != `aws_subnet.this[0]` {
		t.Errorf("the digit-keyed marker decodes to %s, want the documented count reading", addr)
	}
}

// TestMarkerRoundTripsUnderRandomKeys is the same pair of properties over
// keys nobody would think to write down. Only characters a marker can legally
// carry are generated: a key the grammar cannot carry is what RA.3's lint rule
// exists to keep out of an estate, and is not this function's problem - but it
// must be REFUSED rather than mangled into something that parses as a
// different address, which is the assertion in the rejection branch.
func TestMarkerRoundTripsUnderRandomKeys(t *testing.T) {
	rng := rand.New(rand.NewSource(0xDECAF)) //nolint:gosec // test input generation, not cryptography
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_+=@/"

	digits := regexp.MustCompile(`^[0-9]+$`)
	seen := map[string]string{}
	refused := 0
	for i := 0; i < 20000; i++ {
		n := 1 + rng.Intn(24)
		var key strings.Builder
		for j := 0; j < n; j++ {
			key.WriteByte(alphabet[rng.Intn(len(alphabet))])
		}
		addr := fmt.Sprintf("aws_subnet.this[%q]", key.String())

		escaped := EscapeAddress(addr)
		if !ValidMarkerAddress(escaped) {
			if back, ok := UnescapeAddress(escaped); ok {
				t.Fatalf("%s escapes to %q, which the grammar rejects yet still unescapes to %s", addr, escaped, back)
			}
			refused++
			continue
		}

		back, ok := UnescapeAddress(escaped)
		if !ok {
			t.Fatalf("%s escapes to %q, which does not unescape", addr, escaped)
		}
		if again := EscapeAddress(back.String()); again != escaped {
			t.Fatalf("marker %q unescapes to %s and escapes back to %q", escaped, back, again)
		}
		if got := back.String(); got != addr && !digits.MatchString(key.String()) {
			t.Fatalf("%s escapes to %q and comes back as %s", addr, escaped, got)
		}
		if prev, clash := seen[escaped]; clash && prev != addr {
			t.Fatalf("%s and %s both escape to %q", prev, addr, escaped)
		}
		seen[escaped] = addr
	}
	t.Logf("%d random for_each keys round-tripped, all distinct; %d refused by the grammar", len(seen), refused)
}

// TestUnescapeRefusesWhatEscapeNeverProduces is the other direction, and the
// one an attacker controls: a marker is a tag, and anyone with write access to
// the resource can put anything in it. Unescape must refuse every string the
// escaper would not have produced, rather than doing its best with it - a
// best effort here is a resource bound to the wrong address.
//
// Note what is NOT in the list: "AWS_VPC.main". An uppercase type name is a
// perfectly well-formed resource address that happens to name a type nothing
// declares, and refusing it here would be this function deciding which types
// exist - which is discovery's business, one layer up, where it is answered
// against the actual configuration rather than against a character class.
func TestUnescapeRefusesWhatEscapeNeverProduces(t *testing.T) {
	for _, bad := range []string{
		"",
		"aws_vpc",
		"aws_vpc.",
		".main",
		"aws_vpc.main:",
		":aws_vpc.main",
		"aws_vpc.main::0",
		"aws_vpc.main:0:1",
		"aws_vpc .main",
		"aws_vpc.main\n",
		"aws_vpc.main[0]",
		`aws_vpc.main["a"]`,
		"module.net:aws_vpc.main",
		"data.aws_vpc.main",
		"aws_vpc.main.extra",
		"1aws_vpc.main",
	} {
		t.Run(strings.ReplaceAll(bad, "\n", "\\n"), func(t *testing.T) {
			if addr, ok := UnescapeAddress(bad); ok {
				t.Errorf("the marker value %q unescaped to %s; the escaper never produces that string", bad, addr)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Rename beats destroy, whatever order things arrive in
// ---------------------------------------------------------------------------

// TestRenameBeatsDestroyWhateverTheListOrder. Withholding a destroy because
// an orphan might be a renamed key is the single most consequential piece of
// conservatism in the whole sweep: get it wrong and a rename becomes a delete
// and a create. TestRenameBeatsDestroy covers one arrangement; the audit
// listed the ORDERING as what held, and ordering is the part a fake cloud
// with one hand-written list can accidentally fix in place.
//
// The cloud promises no list order. This runs every permutation of an estate
// where one key was renamed and demands the same answer from all of them.
func TestRenameBeatsDestroyWhateverTheListOrder(t *testing.T) {
	// Three live subnets: two still declared, one carrying the marker for a
	// key the configuration no longer has. The fixture declares "a" and "b",
	// so "c" is the orphan and "b" is the unclaimed instance it might be.
	type subnet struct{ id, marker string }
	all := []subnet{
		{"subnet-a", `aws_subnet.this:a`},
		{"subnet-c", `aws_subnet.this:c`},
		{"subnet-z", `aws_subnet.this:z`},
	}

	for i, order := range permutations(len(all)) {
		cloud := newFakeCloud()
		ownWholeEstate(cloud)
		cloud.drop("aws_subnet", "subnet-a")
		cloud.drop("aws_subnet", "subnet-b")
		for _, idx := range order {
			cloud.own("aws_subnet", all[idx].id, all[idx].marker)
		}

		res, diags := discoverFixture(t, cloud, Request{Sweep: true})
		assertNoErrors(t, diags)

		// Two orphans (c and z) against one unclaimed declared instance (b).
		// Ambiguous, so nothing is guessed - and, crucially, nothing is
		// destroyed. Withholding must not be conditional on the guess
		// succeeding, or the case where guessing is least defensible would
		// be the one that deletes.
		if len(res.Orphans) != 2 {
			t.Fatalf("permutation %d %v: want two orphans, got %d:\n%s", i, order, len(res.Orphans), res)
		}
		for _, o := range res.Orphans {
			if o.Removal {
				t.Fatalf("permutation %d %v: an orphan beside an unclaimed instance was proposed for destruction: %s\n%s",
					i, order, o, res)
			}
			if o.Withheld == "" {
				t.Fatalf("permutation %d %v: %s was withheld with no reason given", i, order, o)
			}
		}
		// The one declared instance that is genuinely still there binds,
		// whatever order it was listed in.
		if _, bound := res.BindingFor(mustAddr(t, `aws_subnet.this["a"]`)); !bound {
			t.Fatalf("permutation %d %v: the declared key did not bind:\n%s", i, order, res)
		}
	}
}

// permutations returns every ordering of the indices 0..n-1.
func permutations(n int) [][]int {
	if n <= 1 {
		return [][]int{make([]int, n)}
	}
	var out [][]int
	var walk func(prefix []int, rest []int)
	walk = func(prefix, rest []int) {
		if len(rest) == 0 {
			out = append(out, append([]int(nil), prefix...))
			return
		}
		for i := range rest {
			next := make([]int, 0, len(rest)-1)
			next = append(next, rest[:i]...)
			next = append(next, rest[i+1:]...)
			walk(append(prefix, rest[i]), next)
		}
	}
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	walk(nil, idx)
	return out
}

// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package strict

// Toggle is one entry in this fork's behavior-toggle schema: a `strict`
// block argument's name, what an omitted argument resolves to, and the
// refusal its non-default setting relaxes or tightens relative to stock
// OpenTofu's own behavior. GitHub issue #365's consolidation is this type
// existing at all - before it, a toggle's name lived in
// internal/configs/live.go's decode table, its default and valid spellings
// lived in this package's own constants ([MarkerRepair], [Secrets],
// [NoSourceCreate]), and nothing in the tree said in one place what a given
// toggle traded away or whether it could be pinned. [Toggles] is that one
// place: every toggle this schema defines has exactly one [Toggle] entry,
// and [live/LIMITATIONS.md] cites the same Doc string this type carries.
//
// It deliberately does not replace the typed constants above. Those stay
// the compile-time-checked vocabulary a resolver switches on
// ([StoresSecrets], [CreatesFromNoSource], [Implemented]); this type is the
// runtime-inspectable INVENTORY of that vocabulary, read by the doc
// reference, by [PinRefusal]'s generic lookup, and by anything that needs
// to enumerate every toggle without a hand-written switch over the three
// types.
type Toggle struct {
	// Name is the argument's spelling inside a `strict { ... }` block, the
	// same string internal/configs/live.go's decoder records - "secrets",
	// not "Secrets" or "strict.secrets".
	Name string

	// Default is the literal spelling an omitted argument resolves to,
	// rendered from this package's own DefaultXxx constant so the two
	// cannot drift apart (see TestTogglesDefaultsMatchConstants).
	Default string

	// Relaxes is one sentence naming the refusal a setting other than
	// Default trades away (a compatibility toggle moving further from
	// stock's own behavior) or adds back (a safety toggle moving closer to
	// HANDOFF.md's principles). It is written from the toggle's own
	// non-default setting's point of view, not from Default's.
	Relaxes string

	// Doc is the live/LIMITATIONS.md heading an operator reads for the
	// rule this toggle's typo case fires, the same string ruleInfo's
	// docsRef carries for the lint rule that checks it.
	Doc string

	// Pinnable is whether GitHub issue #365's environment pin ([EnvPin],
	// [Pinned]) can force this toggle to SafeValue from outside the
	// configuration. See [PinRefusal].
	//
	// [MarkerRepair] is not pinnable: its three settings are not a single
	// safety axis the way [Secrets] and [NoSourceCreate] are (a plain
	// "repair" is not less safe than "never", it is a different mechanism
	// paired with a `markers "record"` selection - see
	// internal/live/strict.Implemented's doc comment), so there is no one
	// setting a platform team pinning "the strict profile" could mean by
	// it. A future setting joins the pinnable set only when it has that
	// same single safety axis [Secrets] and [NoSourceCreate] do.
	Pinnable bool

	// SafeValue is the literal spelling [Pinned] forces this toggle to, and
	// the one setting an operator's own `strict.<Name>` argument may not
	// move away from while the pin is active (see [PinRefusal]). Empty
	// when !Pinnable.
	SafeValue string
}

// Toggles is the whole schema: every toggle a `strict` block accepts today,
// in the order internal/configs/live.go's decodeStrictBlock reads its
// arguments. GitHub issue #365's `markers "record"` selection is not an
// entry here - it is a list of types and addresses, not a setting with a
// default and an opposite, so "the refusal it relaxes or tightens" is not a
// single sentence the way it is for the other three (see
// [LiveStrictMarkers] in internal/configs/live.go, and
// live/LIMITATIONS.md's "strict-markers" / "strict-markers-unrecordable"
// for what it does refuse).
var Toggles = []Toggle{
	{
		Name:    "marker_repair",
		Default: string(DefaultMarkerRepair),
		Relaxes: `"never" gives up automatic repair of a drifted ownership marker on the resources a paired ` +
			`markers "record" selection covers, trading marker-based governability for tolerance of an estate ` +
			`where something else owns the tags; "report" is defined but not yet implemented (see [Implemented]).`,
		Doc:      `live/LIMITATIONS.md, "strict-marker-repair"`,
		Pinnable: false,
	},
	{
		Name:    "secrets",
		Default: string(DefaultSecrets),
		Relaxes: `"refuse" tightens the compatible-by-default answer (store secret material the way stock's ` +
			`state file does) into HANDOFF.md's first principle: a secret-generating logical type is refused ` +
			`outright and a sensitive settable argument is never recorded.`,
		Doc:       `live/LIMITATIONS.md, "strict-secrets"`,
		Pinnable:  true,
		SafeValue: string(Refuse),
	},
	{
		Name:    "no_source_create",
		Default: string(DefaultNoSourceCreate),
		Relaxes: `"create" relaxes the default refusal of a no-record, no-marker, no-derivable-identity instance ` +
			`into stock OpenTofu's own behavior for a resource with no prior state: plan a create, and accept the ` +
			`risk that a genuinely new instance and a real one this run cannot see yet are indistinguishable here.`,
		Doc:       `live/LIMITATIONS.md, "strict-no-source-create"`,
		Pinnable:  true,
		SafeValue: string(NoSourceRefuse),
	},
}

// toggleNamed returns the [Toggle] whose Name matches, and whether one was
// found. Linear search over three entries is not worth a map: the table is
// read at lint time, once per strict-block argument, never in a hot loop.
func toggleNamed(name string) (Toggle, bool) {
	for _, t := range Toggles {
		if t.Name == name {
			return t, true
		}
	}
	return Toggle{}, false
}

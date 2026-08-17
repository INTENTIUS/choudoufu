# The harness

What this fork is driving down, and what it believes while it does.

Both halves are registries in `internal/live/harness`, and everything below
between a `harness-gen` marker pair is rendered from them by
`go run ./tools/harness-gen` (`just harness`). No figure here is typed by
hand; each one comes from the measurement function that owns it, run against
the committed artifacts at generation time. Change an artifact without
re-rendering and `TestHarnessDocIsCurrent` goes red, which is deliberate:
one of these ratchets sat above its own ledger for two separate batches
because the constant and the file it bounded had no way to disagree out loud.

The prose around the spans is hand-written and must stay free of figures.
`TestHandWrittenProseCarriesNoFigures` enforces that.

## Burndown

Each entry is a quantity with a direction and a bound. Two rules the entries
have to answer for, both of which came from an incident rather than from
taste:

**A ratchet pins its denominator.** Every count here is against some roster,
and shrinking the roster is always the cheaper way to make the count fall.
Each entry names the roster and a floor, or states in writing that it has no
roster to shrink. The floor is checked before the bound, and a roster below
its floor stops the measurement rather than merely failing it: a count
against a roster that shrank is not a smaller problem, it is a different
measurement.

**A ratchet does not measure itself.** A number read out of an artifact and
checked against that same artifact agrees with itself whatever it says. Each
entry names what it measures and the external thing it is held to, and the
registry refuses an entry where those are the same.

Each entry also records its instrument and what that instrument cannot see.
That field is not decoration. `tools/refusal-probe`'s default mode cannot see
the stamp layer, cannot see any rule that returns false without provider
schemas, and reports every resource of a non-AWS estate as unadmitted - and
"upper bound" is true of its site counts and false of its verdicts, because
blocked configurations *rise* when schemas are supplied. A reader who does
not know an instrument's blind spots will read its zeroes as evidence.

<!-- harness-gen:begin burndown -->
| quantity | now | bound | denominator | tracker |
| --- | ---: | ---: | --- | --- |
| [`mapping-unclassified`](#mapping-unclassified) | 13 | at most 13 | `live/mapping.json row count` at 1699, floor 1600 | #53 |
| [`markerless-veto-admitted-overlap`](#markerless-veto-admitted-overlap) | 0 | at most 0 | `internal/live/identity.MarkerlessTypes` at 141, floor 100 | #249 |
| [`rowgen-annotation-rulings`](#rowgen-annotation-rulings) | 96 | at most 96 | `live/rowgen-convergence.json summary.admitted_total` at 905, floor 850 | #132 |
| [`rowgen-unannotated-mismatches`](#rowgen-unannotated-mismatches) | 0 | at most 0 | `live/rowgen-convergence.json summary.compared` at 891, floor 800 | #132 |
| [`unreached-types`](#unreached-types) | 615 | at most 615 | `live/survey-full.json counts.types` at 1699, floor 1600 | #245, #246 |

<a id="mapping-unclassified"></a>
### `mapping-unclassified`

No row in live/mapping.json is a shrug: a via:"none" row with only the generic unexplained note, meaning nobody has said either what CloudFormation type it corresponds to or why it corresponds to none.

Now **13 unclassified rows**, at most **13**. At the bound.

recomputed from 1699 rows and it agrees with counts.unclassified.

- Measured on live/mapping.json counts.unclassified.
- Held against the artifact's own rows, and live/survey-full.json for the roster size. The bound is checked against a count recomputed from the rows rather than against the summary field, so a summary that disagrees with its own body fails instead of passing. The denominator is pinned to the provider survey, which mapping-gen does not write.
- Instrument: the committed mapping artifact read as JSON. Deliberately not a regeneration: tools/mapping-gen's TestMappingJSONMatchesCommittedInputs already ties the artifact to its inputs, and this ratchet stays independent of that test's shape.
- Denominator `live/mapping.json row count`, measured at 1699 against a floor of 1600. The unclassified count is a subset of the rows, so dropping TF types from the mapping roster lowers it without classifying anything. The row count must also equal the provider survey's own type count, which is what makes this floor external to mapping-gen rather than a second number mapping-gen writes.

What the instrument cannot see:

- It counts rows nobody has classified, not rows classified wrongly. A row folded onto the wrong parent reads as classified here.
- The taxonomy's three terminal buckets (tf-only, cfn-unmodeled, deprecated-service) are classifications, so moving a row into one of them lowers this count without teaching anything new about the type.

Where the bound has been:

- 754 via:"none" rows before the first classification pass, 713 after.
- 13 today, with the family sweeps landed and enforceNoBareNone on.

<a id="markerless-veto-admitted-overlap"></a>
### `markerless-veto-admitted-overlap`

No row in internal/live/identity.DefaultTable names a type the derived markerless rule vetoes. A row for a vetoed type is the shipped table contradicting a derived veto, and it subtracts from unreached-types without anything supporting it.

Now **0 types in both the admission table and the markerless veto**, at most **0**. At the bound.

veto reason: the provider mints this type's identity and the type has no tags argument, so every instance would need marker discovery to be found again and there is nowhere to write the marker.

- Measured on internal/live/identity.DefaultTable.
- Held against internal/live/identity.MarkerlessTypes, and through it live/survey-full.json's signals.taggable. The two rosters are different derivations from different evidence even though one -emit run writes both: the table's rows come from the ratified rows plus the import-doc grammar, the veto from the provider survey's own taggability signal. internal/live/stamp's TestPinnedTaggabilityMatchesTheSurvey ties that signal to the run-time marker writer, so the chain ends at the provider schema rather than at another row-gen output.
- Instrument: two in-process Go maps intersected. No artifact, no provider, no network.
- Denominator `internal/live/identity.MarkerlessTypes`, measured at 141 against a floor of 100. The overlap goes to zero two ways: by retracting the offending rows, which is the point, or by emptying the veto roster, which is not. The rule vetoes 150 types on the pinned release and that population is a property of how many provider types have no tags argument, so a collapse to double digits is a rule change and not a provider one.

What the instrument cannot see:

- It cannot see a type the rule should veto but does not - it bounds the contradiction, not the rule's recall.
- A row pasted by hand for a vetoed type is what this catches; tools/row-gen's PROPOSE stage has never been able to offer one, so the generated path is not the risk.

Where the bound has been:

- 77 for as long as the rule was applied only to what may be admitted next, while 77 rows an earlier batch let through stayed in the table.
- 0 on 2026-08-16, once -emit filtered the emitted rows by the same roster. Zero is the ceiling and the floor: a non-zero count means a row reached the table by a route -emit does not filter.

<a id="rowgen-annotation-rulings"></a>
### `rowgen-annotation-rulings`

tools/row-gen/annotations.json is a list of named extractor gaps that only ever shrinks. With unruled mismatches held at zero, nothing else stops the ledger growing, because adding a ruling is always easier than fixing an extractor.

Now **96 rulings**, at most **96**. At the bound.

every ruling names one of the 891 types the convergence artifact carries, over 905 admitted types.

- Measured on tools/row-gen/annotations.json.
- Held against live/rowgen-convergence.json. Every ruling has to name a type the convergence artifact compared or lists as unmapped, and row-gen writes that artifact from the shipped table rather than from the ledger. A ruling for a type nothing compares is a ruling nothing can retire.
- Instrument: the committed ledger read as JSON, cross-checked against the committed convergence artifact's type list.
- Denominator `live/rowgen-convergence.json summary.admitted_total`, measured at 905 against a floor of 850. The cheapest way to delete a ruling is to un-admit the type it names, which moves the type into tools/row-gen/rejected.json and lowers this count while removing support. Pinning the admitted total makes that trade visible.

What the instrument cannot see:

- Size is not quality. A ruling whose Exit names no reachable fix counts the same as one that does; tools/row-gen's TestAnnotationsAgreeWithMismatches is what forbids a stale one.
- Like the mismatch count, this is generator-autonomy debt. It ranks no user-visible work.

Where the bound has been:

- 128 at the ratchet's introduction: 107 genuine mismatches plus 21 types with no proposal to compare.
- 122, 119, 116 through 2026-08-15 and 16 as classifyUnmapped, tryDocumentedShorterForm and the plain-prose enumeration signal each retired a batch of rulings.
- 95 once the ten record-backed effects rows were derived inside -emit instead of carried as unreproduced table rows. That bump also recorded that the constant had already been stale by nine, which is the failure a ratchet is supposed to make visible.
- 93 on 2026-08-16 when this entry was migrated into the harness: the committed ledger was already two below its own const, so for the second time in two days the number was not bounding anything. Nothing was found to have deleted the two; the const was lowered to the measurement rather than the measurement explained.
- 92 the same day, and this one is accounted for. The cloud-singleton admission retired aws_arczonalshift_autoshift_observer_notification_status's ruling, whose own recorded exit condition was "retire when the vocabulary covers an unschemed example that IS a cloud value" - which is exactly the rule that landed. row-gen -convergence demanded the deletion rather than permitting it, and this entry reported the resulting slack within the hour. That is the first time this ledger has fallen for a reason its own annotation predicted.
- 93 on 2026-08-17: the reviewed upward bump this entry's own rule allows for a newly admitted type the classifier cannot reproduce. aws_iam_user_group_membership is the first row whose import ID has a variable number of segments - one per element of a set-typed argument - and every grammar rule in importprecedence.go compares a FIXED segment count against a fixed argument count. The ruling's exit names the missing evidence rather than the missing rule: importdocs-gen scrapes an argument's name and whether it is required, and nothing anywhere in the artifacts says the argument is a collection.
- 96 on 2026-08-17: the same reviewed upward bump, for three types issue #274's markerless-veto two-source exception admits. aws_cognito_risk_configuration, aws_detective_member and aws_lambda_function_event_invoke_config each have a composite CloudFormation primaryIdentifier with no read-only property AND an import-grammar row whose Import section names no server-provided segment - the two independent sources markerless.go now reads agree the identity is argument-built. All three are still classified server-assigned by tryOpaqueOverride: the scrape pinned only the FIRST of several documented import forms on each page, and that one form's example does not split against the registry's composite primaryIdentifier, which is exactly the shape tryOpaqueOverride reads as "the doc shows one opaque value". Each ruling's exit names the same missing capability: keeping every documented import form, not one pinned example, so a composite rule can test the registry's primaryIdentifier against whichever form demonstrates the split.

<a id="rowgen-unannotated-mismatches"></a>
### `rowgen-unannotated-mismatches`

Every admitted row tools/row-gen's classifier fails to reproduce carries a ruling in tools/row-gen/annotations.json naming what a fuller extraction would have to capture. This counts the ones that do not.

Now **0 unruled mismatches**, at most **0**. At the bound.

recomputed from 891 compared rows: 96 unmatched, every one of them named by one of the ledger's 96 rulings.

- Measured on live/rowgen-convergence.json summary.unannotated_mismatches.
- Held against tools/row-gen/annotations.json. The value is recomputed as genuine_mismatches minus annotated and cross-checked against the ledger's own size, so the artifact's summary field cannot be the only witness to its own claim. row-gen writes the artifact; the ledger is hand-authored and reviewed.
- Instrument: the committed convergence artifact plus the committed ledger, both read as JSON. Not a regeneration - tools/row-gen's TestConvergenceArtifactMatchesCommitted is the drift half.
- Denominator `live/rowgen-convergence.json summary.compared`, measured at 891 against a floor of 800. A mismatch count falls when the compared set shrinks. The compared set is the admitted types the mapping reaches, so a loadMapping filter or an un-admission lowers this count without any extractor improving.

What the instrument cannot see:

- This is generator-autonomy debt and not user-visible coverage. tools/row-gen/emit.go:41 copies every field of a ratified row verbatim, so a mismatch changes nothing a user experiences. adopted_unchanged from the same artifact is not coverage either and must not be quoted as such.
- It compares only the mapped set. The types in summary.not_in_mapped_set have no proposal to compare at all and are outside this number - the -emit gate holds them to the same bar separately.

Where the bound has been:

- 241 after the ratify-remainder batch; 215 once importdocs-widen's parse and the import-precedence rules landed; 194 once the fold-row guard came out.
- 114 through issue #132's seven extractor commits, then 0 once every remaining mismatch was ruled and -emit began refusing an unruled one. It stays 0: a new unannotated mismatch is either a regression or an unruled admission.

<a id="unreached-types"></a>
### `unreached-types`

Every type the pinned provider serves is in one of three rosters - admitted by internal/live/identity.DefaultTable, vetoed by hand in tools/row-gen/rejected.json, or vetoed by the derived markerless rule. This counts the ones in none of them, where naming the type in a configuration is a hard resolve error with no ledger entry saying why.

Now **615 provider resource types**, at most **615**. At the bound.

905 admitted, 80 hand-vetoed, 141 markerless-vetoed, over a roster of 1699.

- Measured on internal/live/identity.DefaultTable, tools/row-gen/rejected.json and internal/live/identity.MarkerlessTypes.
- Held against live/survey-full.json. tools/survey-gen writes it from the provider's own GetProviderSchema response, and none of the three rosters under test contributes a type to it. No edit to the admission table or either veto ledger can make this measurement agree with itself.
- Instrument: the three rosters read in process (two Go maps and one committed JSON file) against the committed provider survey. No provider plugin, no network.
- Denominator `live/survey-full.json counts.types`, measured at 1699 against a floor of 1600. This count is a difference against the roster, so deleting rows from live/survey-full.json lowers it exactly as effectively as admitting a type does, and is the cheaper edit. hashicorp/aws has never lost a hundred resource types in a release.

What the instrument cannot see:

- It counts hard resolve errors only. internal/live/lint's schema fallback (identity.SynthesizeTypeIdentity) admits some of this population at run time when a real provider plugin is present - 60 of them when the count stood at 669 - and that rescue needs a plugin, so a ratchet that subtracted it could not run in the fast tier.
- It describes one provider. A type from any other provider is outside both the roster and this number.
- It says nothing about whether an admitted row is correct, only that the type was reached.

Where the bound has been:

- 669 while the hand ledger stood at 949/81 and again at 944/86 - the batch that moved five types from the ledger into the table did not change this count at all, which is why it exists rather than a count of the ledger.
- 665 when the markerless rule landed (#249); 649 while that rule read only the CloudFormation registry's verdict.
- 621 once tools/importdocs-gen's soleid scrape settled 28 untaggable types the registry models nothing for.
- 615 on 2026-08-17, three of them from single ratifications rather than a batch. The last is aws_s3control_storage_lens_configuration, and it is the first row admitted with no annotation over a documented account-id slot: a composite proposal now reads the cloud_default the argument's own bullet states and renders the segment as a Cloud component, so the classifier reproduces the ratified row instead of needing a ruling for it.
<!-- harness-gen:end burndown -->

## Assumptions

Load-bearing claims the project relies on while it measures anything. The
field that matters is the consequence: "this is true" is worth much less
than "if this stops being true, here is what becomes wrong". Every claim
below has a check behind it in `internal/live/harness`, and the registry
refuses an entry that has a claim but no check, or a claim but no
consequence.

<!-- harness-gen:begin assumptions -->
<a id="checked-layers-are-lint-identity-dataread-stamp"></a>
### `checked-layers-are-lint-identity-dataread-stamp`

Everything an offline report says is derived from four fully checked analysis passes - lint, identity, dataread and stamp - plus projection, which is checked only where it needs no cloud, and discovery, which is not checked at all. All three lists are named rather than omitted.

**If this stops being true.** A clean verdict is a narrow claim, and how narrow is exactly this list. A pass added to internal/live and joined to neither list makes every clean count overstate, silently, in the direction that looks like progress. This is the shape that has appeared three times (#156, #164, #171): a check whose unit does not match the unit of the thing it guards.

- `checked: lint, identity, dataread, stamp`
- `partial: projection (2 of 27 refusals)`
- `unchecked: discovery`

Evidence: internal/live/check/catalog.go's CheckedLayers, PartiallyCheckedLayers and UncheckedLayers, cross-checked against all three of the committed corpus artifact's own header lists, share included. internal/live/check's TestLayersClassifyEveryLivePackage is what forbids a new package joining no list; this holds the three lists themselves to their recorded contents. Projection moved from unchecked to partial when #224's two exported provider-free entry points finally got a caller. Discovery stays wholly unchecked, and #261's plan to move it was measured and refused: of the four refusals its provider-free declared scan can raise, two are caller-bug guards check.Analyze cannot trip, one ("One marker value for two declared addresses") needs two declared addresses escaping to one marker value, which markerkey's excluded runes and #178's reversible key escaping make unreachable for anything identity resolves, and the fourth measures the same quantity as lint.RuleOverlongAddress, an already fully checked layer - see internal/live/check's TestLintCoversTheDeclaredScan.

Tracker: #102

<a id="credential-exclusions-are-exactly-four"></a>
### `credential-exclusions-are-exactly-four`

Exactly four provider types are excluded from admission on credential-material grounds, they are all in the hand veto ledger, and none of them is admitted.

**If this stops being true.** Type parity is the bar, and the credential exclusion is its one sanctioned hole. A fifth type vetoed on credential grounds is admission debt wearing policy's clothes, and it shrinks the parity denominator without anybody deciding to. This has already drifted once in the other direction: aws_secretsmanager_secret_version sat on tools/survey-gen's ops-excluded list reading "credential" until the 2026-08-16 ruling that the marker goes into a tag and never into the secret.

- `aws_appstream_directory_config`
- `aws_iam_access_key`
- `aws_iot_certificate`
- `aws_ivs_playback_key_pair`

Evidence: CLAUDE.md's sanctioned list, checked against tools/row-gen/rejected.json's own reason text and against internal/live/identity.DefaultTable. See credentialReason for what the text half of this cannot see.

Tracker: the parity ruling; no issue - the list is a standing exclusion, not work.

<a id="measurement-artifacts-are-commit-dated"></a>
### `measurement-artifacts-are-commit-dated`

Every committed measurement artifact whose numbers get quoted is tracked and reachable from HEAD, so any figure taken from one can be dated to a commit.

**If this stops being true.** A number that cannot be dated outlives the tree it describes. A site total measured on a branch was propagated into three committed files and was wrong by exactly the size of a class a later merge had emptied; the one copy that survived contact was the one that named its commit. An artifact regenerated and quoted but not committed is the same failure with nothing at all to point at.

- `live/corpus-refusals.json`
- `live/rowgen-convergence.json`
- `live/mapping.json`
- `live/survey-full.json`
- `live/cohort-acceptance.json`
- `live/identity-sources.json`

Evidence: git ls-tree over HEAD. None of these artifacts carries a commit field of its own, so the tree is the only date available - which is itself worth knowing, and is why the quoting rule is to name the commit rather than the file.

Tracker: no issue; this is the measuring-the-wall skill's first instruction made executable.

<a id="onboarding-non-blocking-ids"></a>
### `onboarding-non-blocking-ids`

check.ClassifyOnboarding treats exactly four refusal IDs as something other than language-blocked - the state backend, an unadmitted type, a logical resource and an eligible pre-plan data read - and every other refusal the live path can produce puts an estate on the language-blocked rung.

**If this stops being true.** Every ranking of which configurations are close to working under live markers is computed from this classification. A fifth non-blocking ID moves estates up the ladder with no configuration becoming any more applyable, which is exactly what the markerless retraction had to avoid; a missing one buries work that is nearly done. Three agents re-implemented this classifier in Python on separate days to check it, which is the cost of it living in a switch statement nothing asserts.

- `Resolves at plan time via a data-source read lands on data-read-eligible`
- `logical-resource lands on admissions-only`
- `state-backend lands on backend-only`
- `unadmitted-type lands on admissions-only`

Evidence: internal/live/check/ladder.go's own switch, driven rather than read.

Tracker: #179 for the data-read rung; #102 for the ladder.
<!-- harness-gen:end assumptions -->

## Adding to either registry

Add the entry to `Burndown()` or `Assumptions()` in
`internal/live/harness`, keeping the slice ordered by ID, then run
`just harness`. The shape checks will tell you what is missing; they are
deliberately blunt about it, because an entry that looks complete and proves
nothing is the thing this exists to replace.

Moving a bound down is routine and needs no ceremony beyond re-rendering.
Moving one up is admitting something got worse, and belongs in the entry's
own history with the reason.

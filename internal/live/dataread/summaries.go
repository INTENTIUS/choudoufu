// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package dataread

// The phase's summaries, one per class from #179's design. They are
// constants because every raise site and every consumer (the check layer
// re-homes identity's data-source refusals under them) must agree on the
// exact string.
const (
	// SummaryCrossStackOutputsUnavailable is tfe_outputs' own failure
	// class, distinct from the generic provider-not-configurable and
	// read-failed classes because the cause is almost always the auth
	// surface rather than the provider block itself: no token argument, no
	// TFE_TOKEN, and no CLI credentials entry for the host (caught offline,
	// as part of eligibility - see [analyzer.tfeAuthAvailable]), or the
	// read itself failing with a workspace-not-found, no-current-state, or
	// permission error (caught at read time, quoted from the provider).
	SummaryCrossStackOutputsUnavailable = "Cross-stack outputs unavailable"

	// SummaryCrossStackStateUnavailable is terraform_remote_state's own
	// failure class (#179 stage 3), the backend analog of
	// [SummaryCrossStackOutputsUnavailable]: the backend it names could not
	// be reached, has no state for the named key or workspace, names a
	// backend type this binary does not link, or holds a state snapshot
	// this fork cannot decode (a newer format, or encryption it cannot
	// open) - always caught at read time, quoted from the backend, never
	// guessed at offline. Eligibility (rule 1) still requires the data
	// source's own backend and config arguments to be statically
	// evaluable; only the backend's actual reachability is deferred to read
	// time, per the same ruling [SummaryCrossStackOutputsUnavailable]
	// documents.
	SummaryCrossStackStateUnavailable = "Cross-stack state unavailable"

	// SummaryNotReadable is eligibility failing: the data source's own
	// arguments, its count/for_each, or something it depends on cannot be
	// evaluated before the plan, so there is nothing honest to read.
	SummaryNotReadable = "Data source not readable before resolution"

	// SummaryProviderNotConfigurable is eligibility rule 3, or configure
	// failing at read time: the data source's provider configuration needs
	// more than static evaluation, or the provider's own configure call
	// refused (bad or missing credentials land here).
	SummaryProviderNotConfigurable = "Data source provider not configurable"

	// SummaryReadFailed is the provider returning an error from the read
	// itself, quoted verbatim. Fatal for the run, the same rule resolution
	// applies to identity holes: a partial identity map plans to create
	// things that exist.
	SummaryReadFailed = "Data source read failed"

	// SummaryProviderNotLive is the root-output read class's own boundary
	// (see [LiveProviders]): the data source is readable by every other rule
	// this phase draws, but its provider manages no live object in this
	// configuration, so this run is not already reading the live system
	// through it. Scoped, never fatal - it costs one root output its prior
	// value and nothing else. It is raised only for an output-demanded
	// source; identity demand does not draw this line, because a source
	// identity needs is one the run must have or refuse.
	SummaryProviderNotLive = "Data source provider manages no live object here"

	// SummaryOutOfScope is GitHub issue #352's targeting boundary: the data
	// source is outside the set of blocks this run's -target or -exclude
	// leaves in the plan graph, so the plan will not read it and neither
	// does this phase. Never fatal for either demand class - see
	// [Source.OutOfScope].
	SummaryOutOfScope = "Data source outside this run's -target scope"

	// SummaryEligibleRead is not a refusal: it is live-check's finding for
	// a site the phase will resolve at plan time with a read. It lives in
	// this registry so the corpus and the generated documentation can name
	// it, and so an estate whose only language findings are these lands on
	// the ladder's data-read-eligible rung rather than language-blocked.
	SummaryEligibleRead = "Resolves at plan time via a data-source read"
)

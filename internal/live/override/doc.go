// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package override carries provider behaviour that users of a stock provider
// have wanted for years and that has not landed upstream, without forking or
// patching the provider (GitHub issue #2309).
//
// choudoufu's engine sits between the configuration and the provider on
// every call. [Wrap] puts a [providers.Interface] around each provider the
// command starts, and the overrides registered for that provider act on the
// calls that reach it. The seam is the provider call path, so an override
// works the same with the live record store, an S3 state file or local
// state, on the old runtime and the new one.
//
// # The rules every override keeps
//
//   - It is on by default and listed in every plan it changed: a warning
//     names it, the upstream issue it stands in for and how to turn it off.
//     CHOUDOUFU_PROVIDER_OVERRIDES=off turns every override off for a run;
//     a comma-separated list of -name entries turns off only those.
//   - It adds nothing to the provider's schema and writes nothing to state
//     that stock OpenTofu would not write itself, so turning it off, or
//     going back to stock OpenTofu, brings the old behaviour back and
//     nothing else.
//   - It declares the attributes it reads with their types ([Override.Requires]).
//     When the loaded provider's schema disagrees, the override is skipped
//     for that run and a warning says so. A provider release that renames
//     or retypes an attribute switches the override off rather than making
//     it act on something it was not written for.
//   - It records the upstream issue it stands in for and the provider
//     version it was proven against. When the upstream issue closes, the
//     override is retired, not kept.
//   - It is proven by a test that fails with the override off and passes
//     with it on ([TestEveryOverrideHasAProof]).
//
// # Approaches
//
// The letters are #2309's: A rewrites config or planned values going into a
// call, B rewrites a diff, C adds a step around a call or retries one, D
// refuses a call, E handles one operation of one type itself.
//
// B is implemented as a request rewrite, not a response edit. When a
// configured value is equivalent to the prior value but spelled differently
// (a reordered set held in a list, a value the API normalises), the prior
// spelling is put into the request's Config and ProposedNewState and the
// provider plans as it always does. The provider then computes every other
// attribute itself, including the unknowns a replace would have introduced,
// so the plan is the provider's own no-op rather than a response edited
// after the fact. The planned value then differs from configuration for the
// rewritten attribute, which OpenTofu accepts from SDKv2 providers (it logs
// the mismatch, as it does for the provider's own DiffSuppressFunc) and
// refuses from Plugin Framework ones. That is why every override records
// which SDK its resource type is built on.
package override

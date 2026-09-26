# The claims, one page each

One page per promise: what it promises, what each step of each of its
scenarios does, and what the `BREAK=1` run corrupts. The index, with which
platform each promise is proven on, is `../claims.json`, rendered at
https://intentius.io/choudoufu/docs/claims/.

Each page is named for its claim's slug. A claim is a promise, and proof is
per provider (#1112): each provider cell in `claims.json` names its own
scenario under `../scenarios/`. A promise proven on more than one provider
has a `## On AWS` and a `## On Kubernetes` section, each with its own
command. `live/smoke_claims_test.go` fails if a claim has no page here, if a
page has no claim, or if a page (or a provider's section) never tells the
reader the command that runs it.

Claims 21, 22 and 23 were Kubernetes proofs of claims 7, 1 and 13 filed as
claims of their own. Their pages here are stubs pointing at those sections,
and their numbers are not reused.

These pages were on the docs site until #1414. Each old URL redirects here.

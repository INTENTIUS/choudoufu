# The claims, one page each

One page per smoke claim: what it promises, what each step of its scenario
does, and what the `BREAK=1` run corrupts. The index, with which platform
each claim is proven on, is `../claims.json`, rendered at
https://intentius.io/choudoufu/docs/claims/.

Each page is named for its claim's slug, and `../scenarios/<slug>.sh` is the
script it describes. `live/smoke_claims_test.go` fails if a claim has no page
here, if a page has no claim, or if a page never tells the reader the command
that runs it.

These pages were on the docs site until #1414. Each old URL redirects here.

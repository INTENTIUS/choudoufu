# The claims, one page each

One page per promise: what it promises, what each of its proofs shows, and
what each `BREAK=1` run corrupts. The index, with which platform each
promise is proven on, is `../claims.json`, rendered at
https://intentius.io/choudoufu/docs/claims/.

A claim is a short promise of at most a dozen words (#1817); anything with
a step in it goes on the page. Proof is per provider (#1112): each provider
cell in `claims.json` lists its proofs, each a scenario under
`../scenarios/`, some steps of another claim's scenario, or a gauntlet
stage cited as `<estate>:<stage id>`. A page has a `## On AWS` and a
`## On Kubernetes` section for each cell with proofs, giving the command of
every proof in it, or the stage it cites.

A cell reads `proven` only when every proof in it names a dated run: an
evidence file under `../evidence/` with a `date`, or a stage that reads
pass in `live/gauntlet.json`. Otherwise it reads `unproven`, and its note
names the proofs that have no dated run. No workflow runs these scenarios
since #2018, so a run by hand with its evidence file is what moves a cell. `live/smoke_claims_test.go` fails if a
claim has no page, a page has no claim, or a section never gives a proof's
command.

What each scenario needs: an AWS proof needs Docker and the AWS CLI for the
pinned floci emulator; a Kubernetes proof needs Docker, kind and kubectl,
and the emulator too where its cell says so; a proof marked real AWS is
maintainer-run with `SMOKE_REAL_AWS=1` and refuses to start without it.
Without Go, set `CHOUDOUFU_VERSION` to a release tag; a proof whose
`BREAK=1` rebuilds choudoufu needs Go regardless.

Numbers are never reused. Claims 21 to 23 and, since #1817, 27 more were
folded into the 16 promises left; their pages here are stubs pointing at
the section that carries their proof now. Claim 37 became a demo and keeps
its page. Claims 8, 9 and 44 later merged whole into claims 6, 3 and 28,
whose statements already held theirs; their pages are stubs pointing at
the claim they joined.

These pages were on the docs site until #1414. Each old URL redirects here.

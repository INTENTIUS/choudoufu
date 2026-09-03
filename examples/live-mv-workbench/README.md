# live-mv workbench

An org's own terralith, one estate with everything in it, split into per-team
estates by retagging, on a real AWS account, with the account's own log as
the receipt. This is the demo behind the "tag is the boundary" story, and the
example choudoufu users copy when their monolith is the one to split.

Pinned to one choudoufu release and one account, both named in
`tlmig/config.py` and asserted by preflight before a single call reaches AWS;
every resource a run creates carries the run's prefix, and nothing
destructive runs outside it.

## Where this starts

The stage assumes a terralith: one configuration and one state own
everything, applied by one principal whose permission covers it all, the
way a monolith is run before anyone splits it. The IAM on the map is the
estate's own resources (a role, an inline policy and a managed policy per
team); the operator's permission is the account's, all or nothing. If your
org is not there, the first step differs. Many states already: each one is
adopted as its own estate with `live-import`, and you begin at plan. Per-team operator permissions already: the governance you have is by
state file, and the move and verify phases show the tag-scoped grant that
replaces it. Centralizing first is not required; the boundary is a tag, so
it goes wherever the resources are today.

## Paste-and-go

Hand this to an assistant and let it walk you through:

```text
Clone https://github.com/INTENTIUS/choudoufu and cd examples/live-mv-workbench.
Confirm uv is installed (uv --version). Run:

  uv run --extra viz marimo run workbench.py

A browser page opens on a recording in replay mode. Walk me through it
phase by phase: for each of the eight phases, read me what it does and its
payoff line, and explain what the map or the cost bars changed. If I have
AWS credentials for the account the page names, switch the page to live
and run the phases in order with the buttons, one at a time, waiting for
each to finish; explain each payoff as it appears, and finish with
teardown. If preflight refuses, tell me why from the reason under its
button and stay in replay.
```

## The workflow

The page is eight phases, top to bottom. Each runs one or more verbs of the
`tlmig` CLI; the verbs are the demo's beat names today and the page reads
the installed CLI's verbs from its `--help`, so a rename needs no page
change.

| # | phase | verbs today | what it does |
|---|-------|-------------|--------------|
| 1 | seed | `preflight`, `setup` (`seed` when the CLI has it) | the account check, then the demo apply or your own config adopted; two tags per resource |
| 2 | survey | `slow-plan` | a plan of the whole estate, counted: the number the split brings down |
| 3 | plan | none; the page writes `carve.json` | which address goes to which estate, as a table filled by rules |
| 4 | preview | `preview` (when the CLI has it) | every planned move as a dry run, and the map as it would stand |
| 5 | move | `decompose`, `carve` (`move` when the CLI has it) | the boundary moves: one tag write per resource, no state split |
| 6 | verify | `fast-plan`, `guard` (`verify` when the CLI has it) | plan cost after the split, and both sides plan clean at once |
| 7 | receipt | `receipt` | this run's tag writes read back from CloudTrail |
| 8 | teardown | `teardown` | demo seeds only: every estate destroyed, then the account listed |

Under each phase the demo's own words stay with its verb: the terralith is
"the villain" the survey measures, and the payoff is the fast plan against
it. `tlmig/stage.py` holds the mapping (`WORKFLOW`, `verbs_for`), and the
terminal runs the same verbs in the same order.

## The first minute

Two ways to watch it. **Replay** needs nothing but this checkout: the
notebook opens on a recording, and every phase shows the picture as that
phase left it. **Live** runs each phase for real against the one account
the example is fenced to, so it needs that account's credentials; without
them, preflight refuses and nothing else runs. Pick the mode at the top of
the page.

## What you run

The stage is a marimo notebook. Each phase is a cell: what it does, in two registers, as
narration, a button that runs the phase, the words the run itself says while
it happens, and the picture as the phase leaves it. A live picture near the
top redraws on a timer, so the map moves while you are still talking.

```
uv sync --extra viz
uv run --extra viz marimo run workbench.py     # the stage, in a browser
uv run --extra viz marimo edit workbench.py    # the same file, as a notebook
```

The buttons run the same CLI a terminal would, one phase at a time
(`tlmig/stage.py` holds the one command template), so a phase clicked in
the notebook and a phase typed at a prompt leave the same trail under
`runs/<id>/`, and the picture cannot tell them apart. `--auto` drops the
keypress between verbs because the stage supplies the pacing.

The phases, in story order:

| phase | what happens | what you watch for |
|---|---|---|
| preflight | which account, which binary | the fence: wrong account or wrong release stops here |
| setup | the monolith is applied | every taggable resource comes back carrying `tofu-estate` and `tofu-address`; nobody wrote a tag |
| slow-plan | a full-refresh plan of the whole monolith | the request count: the villain |
| decompose | each team applies its own config | the map recolours by team; nothing re-created, no state split |
| fast-plan | one team's plan from its cache | the request count beside the monolith's and the emulator's reference |
| carve | a role moves between teams with one tag write | its inline policy and attachment follow the parent's live tag, unwritten |
| guard | four reads, one verdict | the light turns green: kept children, both estates plan clean |
| receipt | the account's own record | CloudTrail rows, refusals as `Client.UnauthorizedOperation` against the session refused |
| teardown | each estate destroyed through its own config | the account listed, not trusted: nothing with this run's prefix remains |

## What you see

`tlmig/viz.py` renders a run directory as one picture, stdlib only, the same
HTML in the notebook, in a browser tab (`python -m tlmig.viz runs/<id>`), or
in a test.

- The estate-ownership map. One row per team, one cell per resource, colour
  by the estate its live `tofu-estate` tag names, an outline around each run
  of cells one estate holds. Untaggable children are tied to their parent
  role and take its colour, which is the rule the engine follows. A resource
  the latest listing no longer shows fades out.
- The ledger. Every command the run made and the platform's answer: who,
  action, target, `ok` or the refusal. Reads are dimmed; refusals are red;
  CloudTrail rows join it at the receipt.
- Plan cost. Requests per plan with cache hits, the emulator's numbers as
  grey reference bars.
- The guard. The verdict as a light, both plans' headline counts, the
  children kept.

The picture is a function of files: `manifest.json`, `events.jsonl` (one JSON
object per line, written by `tlmig/events.py`), `receipt.json`, and each
estate's `main.tf`. `viz.load_run(run_dir, upto=N)` replays the first N
events, and `viz.phase_boundaries(run_dir)` gives the index at each phase's
end, which is how the notebook shows a finished run one phase per cell.

## Seed and plan

The notebook opens with a seed panel and, ahead of the move, a planner.
Both work on any estate, not only the demo's.

Seeding is how resources come to carry `tofu-estate` and `tofu-address`.
The demo button applies the terralith config, which was written for
choudoufu, so the tags arrive with the apply. The adopt form takes a config
directory, an optional state file and an estate name, and runs `tlmig seed`
on them: verify first, which is `live-import` without `-approve` (reads every
resource the state names, refuses anything it cannot match, writes nothing),
then adopt, which writes the two tags and nothing else. The state file is
read, never rewritten. The buttons stay off until the installed `tlmig`
has the `seed` verb; the page reads the verbs from `tlmig --help`.

The planner is a table with one row per taggable resource: address, type,
the estate its live tag names, and `to`. Rules fill `to` in bulk, one per
line, `module|prefix|type|name <value> -> <estate>`, later rules winning;
any row can be edited by hand, and `keep` means it stays. Untaggable
children are a count on their parent's row and follow it. Saving writes
`runs/<id>/carve.json`:

```json
{"from": "tlmig-3f9a2c-monolith", "estates": ["team-b"],
 "moves": [{"address": "aws_iam_role.team_a", "from": "tlmig-3f9a2c-monolith", "to": "team-b"}],
 "rules": [{"match": "name", "value": "team_a", "to": "team-b"}]}
```

The move phase reads `moves`; `rules` records how the rows were filled.
The preview button runs every move as `live-mv -dry-run` and the page draws
the map as it would stand once the passed moves are written. The projection
is the page's arithmetic over the dry-run reports, not a choudoufu feature.
`tlmig/carve.py` holds the rules and the file format; `tests/test_carve.py`
proves later rules win, an override wins over rules, and a row already in
its destination is not a move.

## What the receipt proves

In stock Terraform and OpenTofu, who owns a resource is a line in a state
file, and changing that line is state surgery: no IAM policy can gate it,
because the cloud never sees it, and nothing in the account records it. Here
ownership is a tag, a tag write is an API call, and the receipt phase reads
the account's CloudTrail for the carve: the writes that went through, and
the ones a condition on the ownership tag refused, each naming the session
that made the call. The emulator numbers beside the live ones come from
rerunning the `carve-by-retag` claim smoke, so a stranger can reproduce them
without an account.

## Pinning to a local build

While the engine moves, the release a reader can download is not the build
a developer needs to demo against. `CHOUDOUFU_VERSION=local` pins the
example to this checkout instead: the CLI builds `./cmd/choudoufu` from the
repository the example lives in, caches it under `.local/build/<git
describe>/`, and preflight accepts that binary and records its describe as
the build the run measured, in the event feed and on the picture.

```
CHOUDOUFU_VERSION=local uv run tlmig preflight --auto        # builds once
CHOUDOUFU_BIN=/path/to/choudoufu CHOUDOUFU_VERSION=local uv run tlmig setup
```

The notebook has the same switch as its `pin` control. A dirty tree
rebuilds on every run so a change is never demoed stale; a run made this
way says so in its preflight line, because its numbers are this build's,
not the docs'.

## Rehearsing without an account

Two recorded runs live under `tests/fixtures/`: `sample-run`, a synthetic
walk of every phase generated from `tlmig/fixture.py`, and `emitter-run`,
written by the real emitters with the cloud faked at the guard boundary (its
README says what is placeholder and what is real). Pick either from the
notebook's replay menu to rehearse the words against a moving picture.

## Tests

```
uv run python -m unittest discover -s tests
```

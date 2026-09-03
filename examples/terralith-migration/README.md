# terralith-migration

An org's own terralith, one estate with everything in it, split into per-team
estates by retagging, on a real AWS account, with the account's own log as
the receipt. This is the demo behind the "tag is the boundary" story, and the
example choudoufu users copy when their monolith is the one to split.

Pinned to one choudoufu release and one account, both named in
`tlmig/config.py` and asserted by preflight before a single call reaches AWS;
every resource a run creates carries the run's prefix, and nothing
destructive runs outside it.

## What you run

The stage is a marimo notebook. Each phase is a cell: the story beat as
narration, a button that runs the phase, the words the run itself says while
it happens, and the picture as the phase leaves it. A live picture near the
top redraws on a timer, so the map moves while you are still talking.

```
uv sync --extra viz
uv run --extra viz marimo run migration.py     # the stage, in a browser
uv run --extra viz marimo edit migration.py    # the same file, as a notebook
```

The buttons run the same CLI a terminal would, one phase at a time
(`tlmig/stage.py` holds the one command template), so a phase clicked in
the notebook and a phase typed at a prompt leave the same trail under
`runs/<id>/`, and the picture cannot tell them apart. `--auto` drops the
keypress between beats because the stage supplies the pacing.

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

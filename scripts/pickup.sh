#!/usr/bin/env bash
# scripts/pickup.sh: the one command a session runs before touching anything.
#
#   bash scripts/pickup.sh            # read-only; prints the state of the work
#   bash scripts/pickup.sh -no-fetch  # skip the origin fetch (offline)
#
# It exists because a session that crashed, was wound down, or is simply new
# has no memory, and the state of the work is spread over things that are each
# easy to misread alone: the committed artifact, local branches, worktrees
# (some left by the Agent tool under .claude/worktrees/), open pull requests,
# a worker's unread gate files, and the tracker. Reading those by hand has
# been re-derived from scratch every session, and got wrong every time it
# was done from memory (HANDOFF.md, "What a measurement is worth").
#
# Everything printed is READ from git, gh and the tree; nothing is inferred.
# Where the script suggests a disposition for a branch it says which rule
# produced it, and the rules are the ones HANDOFF.md "Pick up here" states.
# It never fetches more than origin, never checks anything out, never deletes.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
REPO="INTENTIUS/choudoufu"
FETCH=1
[ "${1:-}" = "-no-fetch" ] && FETCH=0

# The primary checkout, resolved once (#1012). $ROOT is wherever THIS COPY of
# the script lives - a worktree's checked-out scripts/pickup.sh resolves ROOT
# to that worktree, not to the primary - so anything that must ask about the
# primary specifically (the "dirty" check below; HANDOFF's disposition table
# is keyed on it) has to look past ROOT. `git rev-parse --git-common-dir`
# does that: in a linked worktree it prints the ABSOLUTE path to the
# primary's .git; run from the primary itself it prints the bare relative
# ".git", which is how the two cases are told apart here. A known hard-coded
# path would break for anyone who cloned this repo somewhere else, so it is
# used only as the last-resort fallback, never as the mechanism.
#
# #1142: the fallback to $ROOT is correct for "we are IN the primary", and
# silently wrong for "git could not answer" - a worktree then reports itself
# as the primary checkout. PRIMARY_GUESSED records which of the two happened
# so the dirty line below can say so instead of asserting it.
PRIMARY_GUESSED=""
if COMMON_DIR="$(git rev-parse --git-common-dir 2>&1)"; then :; else
  PRIMARY_GUESSED="git could not resolve --git-common-dir ($(printf '%s' "$COMMON_DIR" | head -1)), so the primary checkout is assumed to be this one"
  COMMON_DIR=""
fi
case "$COMMON_DIR" in
  /*) PRIMARY="$(cd "$(dirname "$COMMON_DIR")" && pwd)" ;;
  *)  PRIMARY="$ROOT" ;;
esac

have() { command -v "$1" >/dev/null 2>&1; }
hr() { printf '\n== %s\n' "$1"; }

# gv runs a git command and prints its output on success. On failure it
# prints "(git failed: <git's own first line>)" and returns 1 (#1142).
#
# git can fail in ways that are not answers, and this script used to read
# every one of them as data. `git log -1 --format=%h -- path` prints nothing
# both for "that path has no history here" and for "git refused to start";
# `git merge-base --is-ancestor` exits 1 for "not an ancestor" and 128 for "I
# could not answer". Anything below that reads git either goes through gv or
# checks the exit status itself, so a broken toolchain reaches the reader as
# a broken toolchain rather than as a blank field or a false finding.
gv() {
  local out rc
  out="$(git "$@" 2>&1)"; rc=$?
  if [ "$rc" != 0 ]; then
    printf '(git failed: %s)' "$(printf '%s' "$out" | head -1)"
    return 1
  fi
  printf '%s' "$out"
}

# One probe before anything reads git, so a broken git is named once at the
# top instead of being inferred from a page of blanks. This is the exact
# failure of 2026-09-15: /usr/bin/git began refusing every invocation with
# "You have not agreed to the Xcode license agreements" after a background
# Xcode update, and pickup reported 29 false DANGLING PROVENANCE rows, a
# blank HEAD, a blank readiness commit and `none` for local branches - every
# line of it wrong, none of it saying so.
GIT_BROKEN=""
if ! git_probe="$(git rev-parse --git-dir 2>&1)"; then
  GIT_BROKEN="$(printf '%s' "$git_probe" | head -2 | tr '\n' ' ')"
fi

# ---------------------------------------------------------------- 1. the tree
hr "tree"
if [ -n "$GIT_BROKEN" ]; then
  printf 'GIT BROKEN every git-derived line below is unreliable and this report is not evidence of anything: %s\n' "$GIT_BROKEN"
  printf '           on macOS this is usually xcode-select pointing at an Xcode whose licence has not been accepted. DEVELOPER_DIR=/Library/Developer/CommandLineTools is the workaround, and unlike a PATH prepend it also reaches the bare exec.Command("git") inside tools/gauntlet.\n'
fi
printf 'checkout   %s\n' "$ROOT"
branch_out="$(git branch --show-current 2>&1)"; branch_rc=$?
if [ "$branch_rc" != 0 ]; then
  printf 'branch     (git failed: %s)\n' "$(printf '%s' "$branch_out" | head -1)"
elif [ -z "$branch_out" ]; then
  printf 'branch     (detached)\n'
else
  printf 'branch     %s\n' "$branch_out"
fi
printf 'HEAD       %s\n' "$(gv log -1 --format='%h %ad %s' --date=short)"
if [ "$FETCH" = 1 ] && git remote get-url origin >/dev/null 2>&1; then
  if git fetch -q origin main 2>/dev/null; then
    ahead=$(git rev-list --count origin/main..main 2>/dev/null || echo '?')
    behind=$(git rev-list --count main..origin/main 2>/dev/null || echo '?')
    printf 'origin     main is %s ahead, %s behind origin/main\n' "$ahead" "$behind"
    [ "$behind" != "0" ] && [ "$behind" != "?" ] && echo '           -> origin/main has commits local main does not: another session pushed. Merge/rebase before doing anything.'
  else
    echo 'origin     fetch failed (offline?); origin/main may be stale'
  fi
fi
# #1142: `git status | wc -l` reads a git failure as "nothing uncommitted",
# which is the most dangerous possible default for this particular line - it
# is the one that tells a session someone else worked in the main tree.
[ -n "$PRIMARY_GUESSED" ] && printf 'primary    ASSUMED: %s\n' "$PRIMARY_GUESSED"
dirty_out="$(git -C "$PRIMARY" status --porcelain 2>&1)"; dirty_rc=$?
if [ "$dirty_rc" != 0 ]; then
  dirty=0
  printf 'dirty      COULD NOT CHECK the primary checkout (%s): %s\n' "$PRIMARY" "$(printf '%s' "$dirty_out" | head -1)"
else
  dirty=$(printf '%s' "$dirty_out" | grep -c . | tr -d ' ')
fi
if [ "$dirty" != "0" ]; then
  echo "dirty      $dirty uncommitted path(s) in the primary checkout ($PRIMARY; a session worked in the main tree; read them before anything else):"
  git -C "$PRIMARY" status --porcelain | head -20 | sed 's/^/             /'
fi

# ------------------------------------------------------------ 2. the artifact
hr "artifact (live/gauntlet.json)"
art_commit=$(gv log -1 --format=%h -- live/gauntlet.json)
head_commit=$(gv log -1 --format=%h)
printf 'last written at %s (HEAD is %s)\n' "${art_commit:-(no history for this path on this branch)}" "$head_commit"
# #1142's follow-up comment: say which copy the numbers below came from. The
# python block reads live/gauntlet.json off the WORKING TREE; in a worktree
# whose own run has just rewritten it, that is not what HEAD holds, and the
# two answer different questions. `git show <commit>:<path>` and a grep of
# the checkout disagreeing silently is how a peer session concluded a run id
# was absent from a file that contained it.
art_diff_out="$(git diff --name-only HEAD -- live/gauntlet.json 2>&1)"; art_diff_rc=$?
if [ "$art_diff_rc" != 0 ]; then
  art_src="the working copy; could not compare it against HEAD (git failed: $(printf '%s' "$art_diff_out" | head -1))"
elif [ -n "$art_diff_out" ]; then
  art_src="the WORKING COPY, which differs from HEAD - the figures below are uncommitted, not what the branch records"
else
  art_src="the working copy, byte-identical to HEAD"
fi
printf 'read from       %s\n' "$art_src"
# Issue #496: the nightly workflow was disabled 2026-09-02 after its PR-open
# step failed for nine straight nights (org policy blocks GITHUB_TOKEN from
# opening PRs, and GAUNTLET_PR_TOKEN's own failure was buried at the bottom
# of a log nobody opened). Surface both halves here, in the one place every
# session already reads: the workflow's enabled/disabled state (via `gh
# api`, since a disabled workflow is not reliably found by name through `gh
# workflow view`) and how long ago the artifact actually landed on this
# branch - the last commit that touched it, not the schedule, which says
# nothing once the workflow stops running.
wf_state=""
if have gh; then
  wf_state=$(gh api "repos/$REPO/actions/workflows" -q '.workflows[] | select(.name=="Gauntlet") | .state' 2>/dev/null)
fi
# #1142: "no output" from git log has two causes - the path genuinely has no
# history here, and git could not run at all - and the old code printed the
# first explanation for both.
art_epoch_out="$(git log -1 --format=%at -- live/gauntlet.json 2>&1)"; art_epoch_rc=$?
if [ "$art_epoch_rc" != 0 ]; then
  when="UNKNOWN - git could not answer: $(printf '%s' "$art_epoch_out" | head -1)"
elif [ -n "$art_epoch_out" ]; then
  art_epoch="$art_epoch_out"
  art_date_only=$(gv log -1 --format=%ad --date=short -- live/gauntlet.json)
  days_ago=$(( ( $(date +%s) - art_epoch ) / 86400 ))
  plural=""; [ "$days_ago" != "1" ] && plural="s"
  when="$art_date_only ($days_ago day$plural ago)"
else
  when="unknown (live/gauntlet.json has no history on this branch)"
fi
case "$wf_state" in
  active) wf_display="active" ;;
  disabled_manually|disabled_inactivity) wf_display="DISABLED" ;;
  "") wf_display="UNKNOWN (gh unavailable or the workflow-state query failed)" ;;
  *) wf_display="$wf_state" ;;
esac
printf 'nightly: workflow %s; artifact last measured %s\n' "$wf_display" "$when"
# #1316: the line above said "active" through twenty consecutive red nights,
# because enabled/disabled is not red/green. Read each nightly's last
# SCHEDULED conclusion, and the nightly-red issues nightly-watch.yml keeps
# open while one is failing. A workflow with no scheduled run in the API's
# retention window prints UNKNOWN rather than a stale green.
if have gh; then
  for wf in gauntlet.yml floci-tier.yml bucket-smoke.yml; do
    last=$(gh run list -R "$REPO" --workflow "$wf" --event schedule --limit 1 \
      --json conclusion,createdAt,url -q '.[0] | "\(.conclusion // "in progress") \(.createdAt[:10]) \(.url)"' 2>/dev/null)
    printf 'nightly %-16s last scheduled run: %s\n' "$wf" "${last:-UNKNOWN (no run in the retention window, or the query failed)}"
  done
  red=$(gh issue list -R "$REPO" --state open --label nightly-red --json number,title -q '.[] | "  #\(.number) \(.title)"' 2>/dev/null)
  if [ -n "$red" ]; then
    printf 'nightly-red issues open (nightly-watch.yml, #1316):\n%s\n' "$red"
  fi
fi
if have python3 && [ -f live/gauntlet.json ]; then
  python3 - <<'EOF'
import json
g=json.load(open('live/gauntlet.json'))
s=g['sets']
# No top-level commit/generated any more (#414: no procedure ever advanced
# them honestly). art_commit above, from git itself, is the real answer to
# "when was this file last written"; each estate's own last_run is the real
# answer to "when did IT last run".
print(f"emulator {g['emulator'].split('@')[-1][:19]}  (current pin - the estate rows below may not all be measured against it)")
print(f"core  {s['core']['clear']}/{s['core']['estates']} clear    all {s['all']['clear']}/{s['all']['estates']} clear" + "".join(f"    {k} lane {v['clear']}/{v['estates']} clear" for k,v in sorted(g.get('lanes',{}).items()) if k=='kubernetes'))
fails=[(e['name'],[k for k,v in e['stages'].items() if v=='fail']) for e in g['estates'] if not e['clear']]
for n,f in fails:
    print(f"  not clear: {n:34} first failing stage: {f[0] if f else '(none failing; a planned stage or not_run)'}")
# Each row's own last_run.emulator is the pin THAT run actually used
# (RunEstates stamps it at run time); g['emulator'] above is only
# configuration for the NEXT run. A clear estate whose last_run.emulator
# differs from the current pin (or never recorded one) is stale evidence,
# not a failure - `gauntlet next` already enqueues it as trailing work, but
# it is otherwise invisible unless read row by row, so name it here too.
stale=[e['name'] for e in g['estates'] if e.get('last_run') and e['last_run'].get('emulator','') != g['emulator']]
if stale:
    shown=', '.join(stale[:8]) + ('...' if len(stale) > 8 else '')
    print(f"stale evidence: {len(stale)} estate(s) last verified against a different (or unrecorded) emulator pin: {shown}")
# Issue #509/#511: last_run.commit is an "as of this commit" provenance
# pointer baked into the artifact at run time. A rebase - or, as #523 found,
# a squash merge - can orphan it with NO textual conflict and no test
# anywhere noticing, because nothing dereferences the hash string against
# real git history except this check and tools/gauntlet's own guard
# (TestEveryLastRunCommitIsAnAncestorOfHEAD). Surfaced here too so a
# dangling pointer is visible in the one place every session already looks,
# not only at CI time.
#
# Issue #1142: this check used to be `returncode == 0`, which collapses
# git's three distinct answers into two. `git merge-base --is-ancestor`
# exits 0 for "ancestor", 1 for "not an ancestor", and something else -
# 128, or 1 from a wrapper - for "I could not answer". On 2026-09-15 a
# machine's git started refusing every invocation and this line reported
# all 29 estate rows as orphaned, every one false. A stop-and-investigate
# finding that fires on its own tooling being broken teaches the reader to
# skim past it, which is precisely what #1012 recorded about the `dirty`
# line two sections up. So: three answers, and an error is never a finding.
import subprocess
def _git(args):
    p = subprocess.run(['git'] + args, capture_output=True, text=True)
    msg = (p.stderr or p.stdout or '').strip().splitlines()
    return p.returncode, (msg[0] if msg else f'git exited {p.returncode}')
probe_rc, probe_err = _git(['rev-parse', '--verify', 'HEAD'])
if probe_rc != 0:
    print(f"PROVENANCE CHECK COULD NOT RUN: git cannot resolve HEAD, so no estate's last_run.commit was dereferenced - this is NOT a finding either way: {probe_err}")
else:
    dangling=[]; unreadable=[]
    for e in g['estates']:
        sha=(e.get('last_run') or {}).get('commit')
        if not sha: continue
        # --verify --quiet distinguishes the two reasons a hash can fail to
        # resolve: exit 1 is "no such object in this checkout" (a real
        # orphan, #509's class in its strongest form), anything else is git
        # failing to answer at all.
        rc, err = _git(['rev-parse', '--verify', '--quiet', sha + '^{commit}'])
        if rc == 1:
            dangling.append((e['name'], sha, 'no such commit object in this checkout'))
            continue
        if rc != 0:
            unreadable.append((e['name'], sha, err)); continue
        rc, err = _git(['merge-base', '--is-ancestor', sha, 'HEAD'])
        if rc == 0: continue
        if rc == 1:
            dangling.append((e['name'], sha, 'not an ancestor of HEAD'))
        else:
            unreadable.append((e['name'], sha, err))
    if dangling:
        shown=', '.join(f"{n} ({c[:10]}: {why})" for n, c, why in dangling[:8]) + ('...' if len(dangling) > 8 else '')
        print(f"DANGLING PROVENANCE: {len(dangling)} estate(s) last_run.commit does not resolve against HEAD's history (issue #509's class - a rebase or squash merge silently orphaned it): {shown}")
    if unreadable:
        shown=', '.join(f"{n} ({c[:10]}: {why})" for n, c, why in unreadable[:4]) + ('...' if len(unreadable) > 4 else '')
        print(f"PROVENANCE CHECK COULD NOT RUN for {len(unreadable)} estate(s): git could not answer, so their provenance is unknown and this is NOT a finding either way: {shown}")
EOF
fi
if have go; then
  if env -u PWD go run ./tools/gauntlet check >/dev/null 2>&1; then
    echo 'rendered docs: current'
  else
    echo 'rendered docs: STALE -> env -u PWD go run ./tools/gauntlet render   (TestRenderedDocsAreCurrent is red until then)'
  fi
fi
if have python3 && [ -f live/readiness.json ]; then
  # #1142: this printed "readiness: ... at " with an empty commit when git
  # failed, which reads as a rendering bug rather than as a broken toolchain.
  r_commit=$(gv log -1 --format=%h -- live/readiness.json)
  r_commit="${r_commit:-(no history for this path on this branch)}"
  python3 - "$r_commit" <<'EOF4'
import json,sys
commit=sys.argv[1]
r=json.load(open('live/readiness.json'))
total=r['counts']['types']
incontract=r['counts']['statuses'].get('in-contract',0)
print(f"readiness: {incontract} in-contract of {total}, at {commit}")
EOF4
fi

# ------------------------------------------------------------- 3. next units
# The full, unfiltered queue: pickup shows every unit so a session sees the
# whole board, not a slice of it. `next -types T1,T2,...` (#436) narrows this
# same queue to estates that exercise the named resource types - useful when
# a change is known to be type-scoped (e.g. after a repin, `next -types
# aws_lambda_function` for a lambda-only fix) - but a type-filtered run is
# never a substitute for this unfiltered one; see live/GAUNTLET.md,
# "Selective re-queue".
hr "next units (env -u PWD go run ./tools/gauntlet next -json -n 6; full text: drop -json)"
if have go; then
  NEXTJSON="$(mktemp -t pickup-next.XXXXXX)"
  env -u PWD go run ./tools/gauntlet next -json -n 6 > "$NEXTJSON" 2>&1
  python3 - "$NEXTJSON" <<'EOF3'
import json,sys
for line in open(sys.argv[1]):
    line=line.strip()
    if not line: continue
    try: u=json.loads(line)
    except ValueError: print("  "+line); continue
    d=(u.get("detail") or "").replace("\n"," ")
    print("  %-44s branch gauntlet/%s-%s  (%s, %d active stage(s) left)" % (u.get("id","?"), u.get("estate","?"), u.get("stage","?"), u.get("set","?"), u.get("remaining",0)))
    if d: print("      detail: %s%s" % (d[:160], "..." if len(d)>160 else ""))
EOF3
  rm -f "$NEXTJSON"
else echo '  go not on PATH'; fi

# -------------------------------------------------------- 4. open pull requests
hr "open pull requests"
PRJSON="$(mktemp -t pickup-prs.XXXXXX)"
if have gh && gh pr list -R "$REPO" --state open --limit 50 \
    --json number,title,headRefName,updatedAt,statusCheckRollup,isDraft > "$PRJSON" 2>/dev/null; then
  python3 - "$PRJSON" <<'EOF2'
import json,sys
prs=json.load(open(sys.argv[1]))
if not prs: print("  none")
for p in prs:
    checks=p.get("statusCheckRollup") or []
    states=[(c.get("conclusion") or c.get("state") or "?") for c in checks]
    if states and all(s in ("SUCCESS","NEUTRAL","SKIPPED") for s in states): ok="green"
    elif not states or any(s in ("PENDING","IN_PROGRESS","QUEUED","EXPECTED") for s in states): ok="pending"
    else: ok="RED"
    print("  #%-5s %-40s ci=%-7s %s  %s" % (p["number"], p["headRefName"][:40], ok, p["updatedAt"][:10], p["title"][:90]))
EOF2
elif have gh; then
  echo '  gh query failed (not logged in, or no network)'
else
  echo '  gh not on PATH'
fi
rm -f "$PRJSON"

# ------------------------------------------------ 5. branches and worktrees
hr "local branches (gauntlet/*, live/*) and their worktrees"
# #1142: an unreadable worktree list used to look exactly like "no branch
# has a worktree", which changes every disposition printed below.
if ! WTLIST="$(git worktree list --porcelain 2>&1)"; then
  printf '  WORKTREE LIST UNAVAILABLE (git failed: %s) - every "worktree", "gate" and "uncommitted" line below is MISSING, not empty\n' "$(printf '%s' "$WTLIST" | head -1)"
  WTLIST=""
fi
wt_of_branch() { # branch -> worktree path or ""
  printf '%s\n' "$WTLIST" | awk -v want="refs/heads/$1" '
    /^worktree /{wt=substr($0,10)}
    /^branch /{if ($2==want) {print wt; exit}}'
}

pr_of_branch() { # branch -> "#N" or ""
  have gh || { echo ""; return; }
  gh pr list -R "$REPO" --state open --head "$1" --json number -q '.[0].number' 2>/dev/null | sed 's/^\([0-9]\)/#\1/'
}

# #1142: the loop used to be fed straight from `git for-each-ref ...
# 2>/dev/null`, so a git that could not run produced no lines, `found`
# stayed 0, and the script printed `none` - indistinguishable from a
# checkout with no branches, and the difference decides whether a session
# thinks there is outstanding work.
REFLIST_ERR=""
if REFLIST="$(git for-each-ref --format='%(refname:short)' refs/heads/gauntlet refs/heads/live refs/heads/wall 2>&1)"; then
  reflist_rc=0
else
  reflist_rc=1
  REFLIST_ERR="$(printf '%s' "$REFLIST" | head -1)"
  REFLIST=""
fi
[ -n "$REFLIST" ] && REFLIST="$REFLIST
"
found=0
anc_rc=0
while IFS= read -r b; do
  [ -z "$b" ] && continue
  [ "$b" = "main" ] && continue
  found=1
  anc_rc=0
  ahead=$(git rev-list --count "main..$b" 2>/dev/null || echo '?')
  behind=$(git rev-list --count "$b..main" 2>/dev/null || echo '?')
  last=$(git log -1 --format='%ad %s' --date=short "$b" 2>/dev/null | cut -c1-80)
  wt="$(wt_of_branch "$b")"
  pr=$(pr_of_branch "$b")
  pr="${pr:-}"
  # #519: a ci.rc file existing and reading 0 does not mean the run that
  # wrote it finished, or that it finished for this worktree's current HEAD.
  # scripts/ci-gate.sh check reads ci.rc AND ci.meta (the run's identity -
  # the sha it was written for) and refuses anything missing, incomplete, or
  # stale, rather than pickup.sh trusting ci.rc's bare content the way it
  # used to.
  gate=""
  if [ -n "$wt" ] && { [ -f "$wt/ci.rc" ] || [ -f "$wt/ci.meta" ]; }; then
    gate="$(cd "$wt" && bash "$ROOT/scripts/ci-gate.sh" check 2>&1)"
  fi
  # Uncommitted work and recent writes: an Agent-tool worker runs inside its
  # parent's process, so no `claude` process names it; the only liveness
  # signal is the worktree itself. ci.out/ci.rc/ci.meta/.bin are a worker's
  # scratch and do not count as work.
  uncommitted=0; recent=""
  if [ -n "$wt" ]; then
    uncommitted=$(git -C "$wt" status --porcelain 2>/dev/null | grep -v -E '^\?\? (ci\.out|ci\.rc|ci\.meta(\.tmp)?|\.bin[^/]*/)$' | wc -l | tr -d ' ')
    if find "$wt" -path "$wt/.git" -prune -o -type f -mmin -15 -print 2>/dev/null | grep -q .; then recent="written in the last 15 min"; fi
  fi
  stage=""
  if [ -n "$wt" ]; then
    est="${b#gauntlet/}"; est="${est%-*}"
    logf="$wt/live/gauntlet/logs/$est.log"
    # A branch named off-convention (not gauntlet/<estate>-<stage>) still has
    # a log if it ran anything; take the newest one and say which.
    [ -f "$logf" ] || logf=$(ls -t "$wt"/live/gauntlet/logs/*.log 2>/dev/null | head -1)
    if [ -n "$logf" ] && [ -f "$logf" ]; then
      stage="$(basename "$logf" .log): $(grep -o 'GAUNTLET stage=[a-z_0-9]* verdict=[a-z_]*' "$logf" | tail -1)"
    fi
  fi
  # disposition, by HANDOFF's rules
  if [ -n "$recent" ]; then
    disp="ACTIVE?       -> files in this worktree were $recent; a worker may be running (Agent-tool workers show no process). Do not touch it; check .claude/scripts/agent-progress.sh or wait"
  elif [ "$uncommitted" != "0" ]; then
    disp="UNCOMMITTED   -> $uncommitted changed path(s) in the worktree and no recent write: a worker stopped before committing. Read the diff, commit it on this branch with the unit ID, then treat as COMMITS, NO PR"
  elif { git merge-base --is-ancestor "$b" main >/dev/null 2>&1; anc_rc=$?; [ "$anc_rc" = 0 ]; } && [ "$ahead" = "0" ]; then
    disp="MERGED/EMPTY  -> delete branch and worktree (ancestor of main with 0 commits ahead, nothing uncommitted, no recent write)"
  elif [ "$anc_rc" != 0 ] && [ "$anc_rc" != 1 ]; then
    # #1142, same shape as the artifact section's provenance check: exit 1
    # is "not an ancestor of main", anything else is git declining to say,
    # and a disposition guessed from a broken git is worse than none.
    disp="UNKNOWN       -> git could not decide whether this branch is merged (exit $anc_rc); read it by hand rather than trusting a disposition"
  elif [ -n "$pr" ]; then
    disp="PR OPEN $pr   -> orchestrator: verify (scripts/ci-gate.sh check, GAUNTLET lines, artifact diff) then merge on green"
  elif [ "$ahead" != "0" ]; then
    disp="COMMITS, NO PR -> resume in its worktree from the last commit; do not start the unit over"
  else
    disp="?"
  fi
  printf '  %-40s ahead %3s behind %3s  %s\n' "$b" "$ahead" "$behind" "$last"
  [ -n "$wt" ]    && printf '      worktree %s\n' "$wt"
  [ -n "$gate" ]  && printf '      gate     %s\n' "$gate"
  [ -n "$stage" ] && printf '      last run %s\n' "$stage"
  if [ "$uncommitted" != "0" ]; then
    printf '      uncommitted %s path(s):' "$uncommitted"
    git -C "$wt" status --porcelain 2>/dev/null | grep -v -E '^\?\? (ci\.out|ci\.rc|ci\.meta(\.tmp)?|\.bin[^/]*/)$' | head -5 | awk '{printf " %s", $2}'
    printf '\n'
  fi
  printf '      %s\n' "$disp"
done < <(printf '%s' "$REFLIST")
if [ "$reflist_rc" != 0 ]; then
  echo "  COULD NOT LIST BRANCHES (git failed: $REFLIST_ERR) - this is not 'none'"
elif [ "$found" = 0 ]; then
  echo '  none'
fi

# Worktrees the Agent tool made (isolation: worktree) live under .claude/worktrees
# and are gitignored; their branches are worktree-agent-*. List them so they
# are not mistaken for nothing.
# #1142: this used to run `git worktree list` again and pipe it straight
# into awk, so the exit status checked was awk's and a git that could not
# list worktrees made the whole section disappear - which reads as "no agent
# worktrees", not as "could not look". It reuses WTLIST above, whose failure
# is already announced once.
agentwt=$(printf '%s\n' "$WTLIST" | awk '/^worktree .*\.claude\/worktrees\//{print $2}')
if [ -n "$agentwt" ]; then
  echo '  Agent-tool worktrees (.claude/worktrees/, branches worktree-agent-*):'
  for w in $agentwt; do
    br=$(git -C "$w" branch --show-current 2>/dev/null)
    ahead=$(git rev-list --count "main..$br" 2>/dev/null || echo '?')
    last=$(git -C "$w" log -1 --format='%ad %s' --date=short 2>/dev/null | cut -c1-70)
    printf '    %-44s ahead %3s  %s\n' "$(basename "$w")" "$ahead" "$last"
  done
  echo '    rule: ahead 0 -> prune (git worktree remove --force <path>); ahead >0 -> read the commits, they are an agent'"'"'s unreported work'
fi

# ------------------------------------------------------------ 6. live processes
hr "processes"
workers=$(pgrep -fl 'claude .*gauntlet-worker' 2>/dev/null | wc -l | tr -d ' ')
printf 'headless claude workers (just contribute): %s   (Agent-tool workers run inside their parent and are NOT listed here; see each worktree line above)\n' "$workers"
if have docker && docker info >/dev/null 2>&1; then
  floci=$(docker ps --format '{{.Names}} {{.Ports}}' 2>/dev/null | grep -i floci || true)
  if [ -n "$floci" ]; then printf 'floci containers:\n%s\n' "$(echo "$floci" | sed 's/^/  /')"; else echo 'floci containers: none'; fi
else
  echo 'docker: not running or not installed (crossing scripts cannot run here)'
fi

# ----------------------------------------------------------- 7. the tracker
hr "tracker: foundation and ruling items (gh issue list ... foundation|ruling)"
if have gh; then
  gh issue list -R "$REPO" --state open --limit 100 --json number,title \
    -q '.[] | select(.title | test("^(foundation|ruling|table|gauntlet stage)")) | "  #\(.number) \(.title)"' 2>/dev/null \
    || echo '  gh query failed'
else
  echo '  gh not on PATH'
fi

hr "what to read next"
cat <<'EOF'
HANDOFF.md "Pick up here" says what each section above means and the rule
for each disposition. Then: .claude/agents/gauntlet-orchestrator.md if you
are running the loop, .claude/agents/gauntlet-worker.md if you are doing one
unit, .claude/agents/live-markers.md for the mechanics and traps of this
checkout. Every number you quote from here names the commit it was read at.
EOF

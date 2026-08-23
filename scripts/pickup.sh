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

have() { command -v "$1" >/dev/null 2>&1; }
hr() { printf '\n== %s\n' "$1"; }

# ---------------------------------------------------------------- 1. the tree
hr "tree"
printf 'checkout   %s\n' "$ROOT"
printf 'branch     %s\n' "$(git branch --show-current 2>/dev/null || echo '(detached)')"
printf 'HEAD       %s\n' "$(git log -1 --format='%h %ad %s' --date=short)"
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
dirty=$(git status --porcelain | wc -l | tr -d ' ')
if [ "$dirty" != "0" ]; then
  echo "dirty      $dirty uncommitted path(s) in the primary checkout (a session worked in the main tree; read them before anything else):"
  git status --porcelain | head -20 | sed 's/^/             /'
fi

# ------------------------------------------------------------ 2. the artifact
hr "artifact (live/gauntlet.json)"
art_commit=$(git log -1 --format=%h -- live/gauntlet.json)
head_commit=$(git log -1 --format=%h)
printf 'last written at %s (HEAD is %s)\n' "$art_commit" "$head_commit"
if have python3 && [ -f live/gauntlet.json ]; then
  python3 - <<'EOF'
import json
g=json.load(open('live/gauntlet.json'))
s=g['sets']
print(f"runner commit {g['commit'][:10]}  emulator {g['emulator'].split('@')[-1][:19]}  generated {g['generated']}")
print(f"core  {s['core']['clear']}/{s['core']['estates']} clear    all {s['all']['clear']}/{s['all']['estates']} clear")
fails=[(e['name'],[k for k,v in e['stages'].items() if v=='fail']) for e in g['estates'] if not e['clear']]
for n,f in fails:
    print(f"  not clear: {n:34} first failing stage: {f[0] if f else '(none failing; a planned stage or not_run)'}")
EOF
fi
if have go; then
  if env -u PWD go run ./tools/gauntlet check >/dev/null 2>&1; then
    echo 'rendered docs: current'
  else
    echo 'rendered docs: STALE -> env -u PWD go run ./tools/gauntlet render   (TestRenderedDocsAreCurrent is red until then)'
  fi
fi

# ------------------------------------------------------------- 3. next units
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
WTLIST="$(git worktree list --porcelain)"
wt_of_branch() { # branch -> worktree path or ""
  printf '%s\n' "$WTLIST" | awk -v want="refs/heads/$1" '
    /^worktree /{wt=substr($0,10)}
    /^branch /{if ($2==want) {print wt; exit}}'
}

pr_of_branch() { # branch -> "#N" or ""
  have gh || { echo ""; return; }
  gh pr list -R "$REPO" --state open --head "$1" --json number -q '.[0].number' 2>/dev/null | sed 's/^\([0-9]\)/#\1/'
}

found=0
while IFS= read -r b; do
  [ -z "$b" ] && continue
  [ "$b" = "main" ] && continue
  found=1
  ahead=$(git rev-list --count "main..$b" 2>/dev/null || echo '?')
  behind=$(git rev-list --count "$b..main" 2>/dev/null || echo '?')
  last=$(git log -1 --format='%ad %s' --date=short "$b" 2>/dev/null | cut -c1-80)
  wt="$(wt_of_branch "$b")"
  pr=$(pr_of_branch "$b")
  pr="${pr:-}"
  gate=""
  if [ -n "$wt" ] && [ -f "$wt/ci.rc" ]; then gate="ci.rc=$(cat "$wt/ci.rc" 2>/dev/null | tr -d '[:space:]')"; fi
  # Uncommitted work and recent writes: an Agent-tool worker runs inside its
  # parent's process, so no `claude` process names it; the only liveness
  # signal is the worktree itself. ci.out/ci.rc/.bin are a worker's scratch
  # and do not count as work.
  uncommitted=0; recent=""
  if [ -n "$wt" ]; then
    uncommitted=$(git -C "$wt" status --porcelain 2>/dev/null | grep -v -E '^\?\? (ci\.out|ci\.rc|\.bin[^/]*/)$' | wc -l | tr -d ' ')
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
  elif git merge-base --is-ancestor "$b" main 2>/dev/null && [ "$ahead" = "0" ]; then
    disp="MERGED/EMPTY  -> delete branch and worktree (ancestor of main with 0 commits ahead, nothing uncommitted, no recent write)"
  elif [ -n "$pr" ]; then
    disp="PR OPEN $pr   -> orchestrator: verify (read ci.rc, GAUNTLET lines, artifact diff) then merge on green"
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
    git -C "$wt" status --porcelain 2>/dev/null | grep -v -E '^\?\? (ci\.out|ci\.rc|\.bin[^/]*/)$' | head -5 | awk '{printf " %s", $2}'
    printf '\n'
  fi
  printf '      %s\n' "$disp"
done < <(git for-each-ref --format='%(refname:short)' refs/heads/gauntlet refs/heads/live refs/heads/wall 2>/dev/null)
[ "$found" = 0 ] && echo '  none'

# Worktrees the Agent tool made (isolation: worktree) live under .claude/worktrees
# and are gitignored; their branches are worktree-agent-*. List them so they
# are not mistaken for nothing.
agentwt=$(git worktree list --porcelain | awk '/^worktree .*\.claude\/worktrees\//{print $2}')
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

# stock-when-you-need-it
# CLAIM 8 - Stock when you need it: stock behavior is the fallback, whole and exact - measured, not promised - and the live backend prices by your estate, not your account. ~3 min.

SMOKE_WORK="$SMOKE_WORKROOT/parity"
mkdir -p "$SMOKE_WORK"; export SMOKE_WORK
cp -R "$ROOT/live/e2e/estate-block/." "$SMOKE_WORK/"
rm -rf "$SMOKE_WORK/README.md"
python3 - "$SMOKE_WORK" <<'PYEOF'
import re, sys
d = sys.argv[1]
src = open(f'{d}/versions.tf').read().replace('stateless-e2e-block', 'smoke-parity')
open(f'{d}/versions-live.tf.keep', 'w').write(src)
stock = re.sub(r'\n  live \{\n    estate = "smoke-parity"\n  \}\n', '\n', src)
assert 'live {' not in stock
open(f'{d}/versions-stock.tf.keep', 'w').write(stock)
PYEOF
use_versions() { cp "$SMOKE_WORK/versions-$1.tf.keep" "$SMOKE_WORK/versions.tf"; }
LOGDIR="$SMOKE_WORKROOT/parity-logs"; mkdir -p "$LOGDIR"
plan_body() { grep -vE '^$|Refreshing state|Reading\.\.\.|Read complete|^Note:|^─|-out option|guarantee to take exactly|^time="|^ Container |already exists but was created|compared your real infrastructure|no changes are needed' "$1"; }

stack_up
export AWS_ENDPOINT_URL="$SMOKE_ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1

step "the claim"
explain \
  "Stock when you need it. Stock behavior is not a mode you leave" \
  "behind - it is the fallback, whole and exact, one deleted live block" \
  "away. That is measured, not promised: a plan over the same estate" \
  "and state file issues exactly the requests the pinned stock OpenTofu" \
  "issues, and prints the same answer. And when the live backend is on," \
  "what you pay scales with your estate, not with the account around" \
  "it."

step "1. a stock estate, stood up by choudoufu with no live block"
explain \
  "The fixture's live block is removed and choudoufu applies the estate" \
  "the ordinary way: a real terraform.tfstate, no markers, no discovery," \
  "no hooks. Nothing below distinguishes this from stock - which is the" \
  "point, and the next step measures it."
cmd "choudoufu apply -auto-approve   # no live block anywhere"
use_versions stock
( cd "$SMOKE_WORK" && chdf init -input=false -no-color >/dev/null 2>&1 ) || fail "parity" "choudoufu init failed"
stock init -input=false -no-color >/dev/null 2>&1 || fail "parity" "oracle init failed"
( cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "parity" "stock-mode apply failed"
[ -f "$SMOKE_WORK/terraform.tfstate" ] || fail "parity" "no terraform.tfstate - stock mode is supposed to write one"
proof "a plain state-backed estate, built by the fork behaving as stock."

step "2. same plan, same requests - the fork against the pinned oracle"
explain \
  "Both tools plan the same estate from the same state file, with debug" \
  "logging on. The plan text must match and so must the request count -" \
  "not roughly, exactly. This is the #588 parity measurement as a" \
  "two-minute demo: none of the fork's machinery runs unless a live" \
  "block asks for it."
cmd "choudoufu plan ; docker compose run opentofu plan   # both with TF_LOG=debug"
if [ "${BREAK:-0}" = "1" ]; then
  explain \
    "BREAK control: the choudoufu leg deliberately runs WITH the live" \
    "block. Asked-for machinery must show up in the measurement - if the" \
    "plan text and request count still match stock, the parity check" \
    "compares nothing."
  use_versions live
  ( cd "$SMOKE_WORK" && chdf init -input=false -no-color >/dev/null 2>&1 ) || fail "parity" "BREAK re-init failed"
fi
( cd "$SMOKE_WORK" && TF_LOG=debug TF_LOG_PATH="$LOGDIR/chdf.log" "$TOFU" plan -input=false -no-color > "$LOGDIR/chdf-plan.txt" 2>&1 ) \
  || { [ "${BREAK:-0}" = "1" ] || fail "parity" "choudoufu plan failed: $(cat "$LOGDIR/chdf-plan.txt")"; }
[ "${BREAK:-0}" = "1" ] && use_versions stock
"${COMPOSE[@]}" run --rm --user "$(id -u):$(id -g)" -e TF_LOG=debug -e TF_LOG_PATH=/work/oracle.log opentofu plan -input=false -no-color > "$LOGDIR/oracle-plan.txt" 2>&1 \
  || fail "parity" "oracle plan failed: $(cat "$LOGDIR/oracle-plan.txt")"
mv "$SMOKE_WORK/oracle.log" "$LOGDIR/oracle.log" 2>/dev/null || fail "parity" "the oracle wrote no debug log"
REQ_CHDF=$(grep -c "HTTP Request Sent" "$LOGDIR/chdf.log" || true)
REQ_ORACLE=$(grep -c "HTTP Request Sent" "$LOGDIR/oracle.log" || true)
[ "$REQ_ORACLE" -gt 0 ] || fail "parity" "the oracle log counted zero requests - the counter is not measuring"
if [ "${BREAK:-0}" = "1" ]; then
  if [ "$REQ_CHDF" = "$REQ_ORACLE" ] && diff -q <(plan_body "$LOGDIR/chdf-plan.txt") <(plan_body "$LOGDIR/oracle-plan.txt") >/dev/null 2>&1; then
    fail "parity" "BREAK: a live-block plan measured identical to stock - the parity comparison compares nothing"
  fi
  echo "with the live block: $REQ_CHDF requests vs stock's $REQ_ORACLE" | evidence
  proof "caught - asking for the live backend showed up in the measurement, so the equality below is a real check."
  use_versions stock
  ( cd "$SMOKE_WORK" && chdf init -input=false -no-color >/dev/null 2>&1 ) || true
  ( cd "$SMOKE_WORK" && chdf apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || true
  exit 0
fi
grep -q "No changes." "$LOGDIR/chdf-plan.txt" || fail "parity" "the choudoufu leg is not converged"
diff <(plan_body "$LOGDIR/chdf-plan.txt") <(plan_body "$LOGDIR/oracle-plan.txt") > "$LOGDIR/plan-diff.txt" \
  || fail "parity" "the plan texts differ: $(cat "$LOGDIR/plan-diff.txt")"
[ "$REQ_CHDF" = "$REQ_ORACLE" ] \
  || fail "parity" "request counts differ: choudoufu $REQ_CHDF vs oracle $REQ_ORACLE"
echo "choudoufu: $REQ_CHDF requests; stock oracle: $REQ_ORACLE. Plan texts equal, filtered of version cosmetics." | evidence
proof "same answer, same wire traffic, measured against the pinned oracle. The hooks cost nothing until asked for."

step "3. the live backend on - and you pay for your estate, not your account"
explain \
  "The stock estate comes down and the live one goes up: same fixture," \
  "live block on, markers riding the creates. Then the account gets" \
  "cluttered with twenty foreign resources that have nothing to do with" \
  "this estate. If discovery cost scaled with the account, the plan's" \
  "request count would grow; it must not move."
cmd "apply (live) ; plan ; create 20 foreign log groups ; plan again"
( cd "$SMOKE_WORK" && chdf apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "parity" "stock estate teardown failed"
rm -f "$SMOKE_WORK/terraform.tfstate"
use_versions live
( cd "$SMOKE_WORK" && chdf init -input=false -no-color >/dev/null 2>&1 ) || fail "parity" "live init failed"
( cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "parity" "live apply failed"
( cd "$SMOKE_WORK" && TF_LOG=debug TF_LOG_PATH="$LOGDIR/quiet.log" "$TOFU" plan -input=false -no-color >/dev/null 2>&1 ) \
  || fail "parity" "live plan (quiet account) failed"
for i in $(seq 1 20); do
  awsl logs create-log-group --log-group-name "/foreign/clutter-$i" >/dev/null 2>&1 || fail "parity" "could not create foreign clutter"
done
( cd "$SMOKE_WORK" && TF_LOG=debug TF_LOG_PATH="$LOGDIR/clutter.log" "$TOFU" plan -input=false -no-color >/dev/null 2>&1 ) \
  || fail "parity" "live plan (cluttered account) failed"
REQ_QUIET=$(grep -c "HTTP Request Sent" "$LOGDIR/quiet.log" || true)
REQ_CLUTTER=$(grep -c "HTTP Request Sent" "$LOGDIR/clutter.log" || true)
[ "$REQ_QUIET" -gt 0 ] || fail "parity" "the quiet-account plan counted zero requests"
echo "quiet account: $REQ_QUIET requests; after 20 foreign resources: $REQ_CLUTTER" | evidence
[ "$REQ_QUIET" = "$REQ_CLUTTER" ] \
  || fail "parity" "account clutter moved the plan's request count ($REQ_QUIET -> $REQ_CLUTTER)"
proof "twenty strangers in the account and the bill did not move. The estate tag scopes every read."

step "4. teardown"
cmd "choudoufu apply -destroy -auto-approve ; delete the foreign clutter"
( cd "$SMOKE_WORK" && chdf apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "parity" "live teardown failed"
for i in $(seq 1 20); do
  awsl logs delete-log-group --log-group-name "/foreign/clutter-$i" >/dev/null 2>&1 || true
done
proof "estate and clutter both gone."

echo "  What you watched: the fork plan a stateful estate with exactly"
echo "  stock's answer and exactly stock's request count, measured against"
echo "  the pinned oracle - then, with the live block on, ignore twenty"
echo "  foreign resources at zero added cost. You pay nothing until you ask,"
echo "  and then only for what you own."

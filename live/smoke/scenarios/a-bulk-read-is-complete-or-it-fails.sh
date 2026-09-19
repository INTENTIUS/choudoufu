# a-bulk-read-is-complete-or-it-fails
# CLAIM 31 - A record read that fails mid-fanout fails the read; a short map never reaches a plan. ~2 min.

SMOKE_WORK="$SMOKE_WORKROOT/bulkread"
BUCKET="smoke-bulkread-records"
N=12
mkdir -p "$SMOKE_WORK/est"; export SMOKE_WORK
cat > "$SMOKE_WORK/est/main.tf" <<TFEOF
terraform {
  live {
    estate = "smoke-bulkread"

    record_store "s3" {
      bucket = "$BUCKET"
    }

    # One attempt per request, so a failed GET reaches the record store as a
    # failure. With the SDK's default of three, a single 500 is retried away
    # below the code this claim is about and nothing here would be measured.
    retry {
      max_attempts = 1
    }
  }
}

resource "terraform_data" "effect" {
  count = $N
  input = "v\${count.index}"
}
TFEOF

# The proxy. It forwards every request to the emulator untouched, except that
# while \$SMOKE_WORK/fail holds "<substring> <count>", a GetObject whose path
# contains the substring is answered with a 500 (count times; -1 is forever).
# That is the only way to fail one GET out of a fan-out from outside the
# binary: the emulator has no fault injection, and nothing in the cloud can be
# corrupted into a 500.
cat > "$SMOKE_WORK/proxy.py" <<'PYEOF'
import http.client, http.server, os, socketserver, sys, threading
upstream_port, work = int(sys.argv[1]), sys.argv[2]
lock = threading.Lock()

def should_fail(path, query):
    if "list-type=2" in query:
        return False
    with lock:
        try:
            sub, count = open(os.path.join(work, "fail")).read().split()
        except (OSError, ValueError):
            return False
        count = int(count)
        if sub not in path or count == 0:
            return False
        if count > 0:
            open(os.path.join(work, "fail"), "w").write("%s %d" % (sub, count - 1))
        return True

class Handler(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    def log_message(self, *a): pass
    def relay(self):
        path, _, query = self.path.partition("?")
        body = self.rfile.read(int(self.headers.get("Content-Length") or 0))
        if self.command == "GET" and should_fail(path, query):
            payload = b'<?xml version="1.0" encoding="UTF-8"?><Error><Code>InternalError</Code><Message>injected by the smoke proxy</Message></Error>'
            self.send_response(500); self.send_header("Content-Length", str(len(payload))); self.end_headers(); self.wfile.write(payload)
            status = 500
        else:
            host = self.headers.get("Host", "localhost").rsplit(":", 1)[0]
            headers = {k: v for k, v in self.headers.items() if k.lower() not in ("host", "connection")}
            headers["Host"] = "%s:%d" % (host, upstream_port)
            conn = http.client.HTTPConnection("localhost", upstream_port, timeout=30)
            conn.request(self.command, self.path, body=body, headers=headers)
            resp = conn.getresponse(); data = resp.read(); status = resp.status
            self.send_response(status)
            for k, v in resp.getheaders():
                if k.lower() not in ("transfer-encoding", "connection", "content-length"):
                    self.send_header(k, v)
            self.send_header("Content-Length", str(len(data))); self.end_headers()
            if self.command != "HEAD":
                self.wfile.write(data)
            conn.close()
        with lock:
            open(os.path.join(work, "proxy.log"), "a").write("%s %s %d\n" % (self.command, self.path, status))
    do_GET = do_PUT = do_DELETE = do_HEAD = do_POST = relay

class Server(socketserver.ThreadingMixIn, http.server.HTTPServer):
    daemon_threads = True
    # choudoufu cancels the sibling GETs the moment one fails, which is the
    # fan-out doing its job, and each of those is a client hanging up on this
    # proxy mid-response. That is not a proxy fault and must not read as one.
    def handle_error(self, request, client_address): pass
srv = Server(("127.0.0.1", 0), Handler)
open(os.path.join(work, "proxy.port"), "w").write(str(srv.server_address[1]))
srv.serve_forever()
PYEOF

step "the claim"
explain \
  "A plan reads the estate's records in one bulk read: a LIST, then one" \
  "GET per record, eight at a time. What comes back is read as complete -" \
  "a declared resource with no record is something to CREATE, and a" \
  "record with no configuration is something to DESTROY. So the worst" \
  "thing that read can do is succeed with a record missing, and running" \
  "the GETs in parallel is exactly where that would come from: a loop" \
  "returns on its first error for free, a fan-out has to be written to." \
  "This claim fails one GET out of $N and follows it all the way to the" \
  "plan, because the harm is a plan, not a map."

# The binary under test. BREAK=1 swaps in one whose fan-out drops a failed
# GET's key and carries on, which is the defect.
RUN_BIN="$TOFU"
if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - a fan-out that drops a failed key must be caught at the plan"
  explain \
    "The corruption is in the binary: the fan-out swallows a failed call" \
    "instead of failing the read. It is built with go build -overlay, so" \
    "the source tree is never touched, and it needs this checkout and Go."
  [ -z "${CHOUDOUFU_BIN:-}${CHOUDOUFU_VERSION:-}" ] \
    || fail "bulkread" "BREAK=1 rebuilds choudoufu from this checkout; it cannot break CHOUDOUFU_BIN or CHOUDOUFU_VERSION. Unset them and run it again with Go installed."
  command -v go >/dev/null 2>&1 || fail "bulkread" "BREAK=1 needs Go to build the broken binary"
  SRC="$ROOT/internal/live/staterecord/bulk.go"
  mkdir -p "$SMOKE_WORK/break"
  python3 - "$SRC" "$SMOKE_WORK/break/bulk.go" <<'PYEOF'
import sys
src = open(sys.argv[1]).read()
old = "\t\t\t\t\tfailOnce.Do(func() {\n\t\t\t\t\t\tfailure = err\n\t\t\t\t\t\tcancel()\n\t\t\t\t\t})\n"
assert src.count(old) == 1, "the break patch no longer matches boundedFanOut"
open(sys.argv[2], "w").write(src.replace(old, "\t\t\t\t\tfailOnce.Do(func() {})\n"))
PYEOF
  [ -s "$SMOKE_WORK/break/bulk.go" ] || fail "bulkread" "the break patch did not apply to $SRC, so this arm would pass by testing the real binary"
  printf '{"Replace":{"%s":"%s"}}\n' "$SRC" "$SMOKE_WORK/break/bulk.go" > "$SMOKE_WORK/break/overlay.json"
  cmd "go build -overlay overlay.json ./cmd/choudoufu   # a failed GET drops its key"
  ( cd "$ROOT" && go build -overlay "$SMOKE_WORK/break/overlay.json" -o "$SMOKE_WORK/break/choudoufu" ./cmd/choudoufu ) \
    || fail "bulkread" "the broken binary did not build"
  RUN_BIN="$SMOKE_WORK/break/choudoufu"
fi
run() { ( cd "$SMOKE_WORK/est" && "$RUN_BIN" "$@" ); }
flat() { tr '\n' ' ' | sed 's/│/ /g' | tr -s ' '; }

step "1. $N record-backed resources, applied straight to the emulator"
stack_up
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1
export AWS_ENDPOINT_URL="$SMOKE_ENDPOINT"
awsl s3api create-bucket --bucket "$BUCKET" >/dev/null || fail "bulkread" "could not create the bucket"
awsl s3api put-bucket-versioning --bucket "$BUCKET" --versioning-configuration Status=Enabled >/dev/null
awsl s3api put-bucket-lifecycle-configuration --bucket "$BUCKET" --lifecycle-configuration '{"Rules":[{"ID":"expire-noncurrent","Status":"Enabled","Filter":{"Prefix":""},"NoncurrentVersionExpiration":{"NoncurrentDays":30}}]}' >/dev/null
awsl s3api put-public-access-block --bucket "$BUCKET" --public-access-block-configuration BlockPublicAcls=true,IgnorePublicAcls=true,BlockPublicPolicy=true,RestrictPublicBuckets=true >/dev/null
run init -input=false -no-color >/dev/null 2>&1 || fail "bulkread" "init failed"
cmd "choudoufu apply -auto-approve"
OUT="$(run apply -auto-approve -input=false -no-color 2>&1)" || fail "bulkread" "apply failed: $OUT"
grep -q "Resources: $N added" <<< "$OUT" || fail "bulkread" "expected $N resources: $OUT"
KEYS="$(awsl s3api list-objects-v2 --bucket "$BUCKET" --prefix tofu-records/smoke-bulkread/terraform_data/ --query 'Contents[].Key' --output text | tr '\t' '\n')"
[ "$(wc -l <<< "$KEYS" | tr -d ' ')" = "$N" ] || fail "bulkread" "expected $N record objects, found: $KEYS"
VICTIM="$(sed -n '7p' <<< "$KEYS")"
echo "$N records; the one this scenario will fail: $VICTIM" | evidence
proof "every resource has a record, and one of them is singled out."

step "2. the proxy, and a control plan through it with nothing failing"
python3 "$SMOKE_WORK/proxy.py" "$FLOCI_PORT" "$SMOKE_WORK" 2>"$SMOKE_WORKROOT/logs/bulkread-proxy.err" &
PROXY_PID=$!
trap 'kill $PROXY_PID 2>/dev/null || true; cleanup' EXIT
for _ in $(seq 1 50); do [ -s "$SMOKE_WORK/proxy.port" ] && break; sleep 0.1; done
[ -s "$SMOKE_WORK/proxy.port" ] || fail "bulkread" "the proxy never started"
export AWS_ENDPOINT_URL="http://localhost:$(cat "$SMOKE_WORK/proxy.port")"
# The state cache would answer the plan without asking the store at all.
rm -f "$SMOKE_WORK/est/.terraform/choudoufu-cache.tfstate"
cmd "choudoufu plan   # through the proxy, nothing armed"
C_OUT="$(run plan -input=false -no-color 2>&1)" || fail "bulkread" "the control plan through the proxy failed: $C_OUT"
grep -q "No changes." <<< "$C_OUT" || fail "bulkread" "the control plan through the proxy was not empty, so the proxy itself changes the answer: $C_OUT"
GETS="$(grep -c '^GET /tofu-records/smoke-bulkread/terraform_data/' "$SMOKE_WORK/proxy.log" || true)"
[ "$GETS" -ge "$N" ] || fail "bulkread" "the proxy saw $GETS record GETs, fewer than the $N records: the plan is not reading records through it, and failing one would prove nothing"
echo "record GETs seen by the proxy: $GETS, all 200" | evidence
proof "the plan reads every record through the proxy and is empty. Whatever changes next is the failure's doing."

step "3. one GET fails once, mid-fanout"
explain \
  "The realistic fault: a throttle, a reset connection. One record's GET" \
  "is answered 500, once. The bulk read must not come back short. It may" \
  "fail, and the run may then read its records one at a time instead, but" \
  "the plan that results has to be the true one."
: > "$SMOKE_WORK/proxy.log"
echo "$VICTIM 1" > "$SMOKE_WORK/fail"
rm -f "$SMOKE_WORK/est/.terraform/choudoufu-cache.tfstate"
cmd "choudoufu plan   # $VICTIM answers 500 once"
T_OUT="$(run plan -input=false -no-color 2>&1)" && T_RC=0 || T_RC=$?
grep -c ' 500$' "$SMOKE_WORK/proxy.log" | sed 's/^/requests the proxy failed: /' | evidence
[ "$(grep -c ' 500$' "$SMOKE_WORK/proxy.log")" -ge 1 ] || fail "bulkread" "the proxy failed nothing, so this step measured an ordinary plan"
if grep -qE 'Plan: [1-9][0-9]* to add' <<< "$T_OUT"; then
  if [ "${BREAK:-0}" = "1" ]; then
    grep -E 'Plan:|will be created' <<< "$T_OUT" | head -3 | evidence
    proof "caught - with the failed key dropped, the read came back one record short and the plan proposes CREATING a resource that exists. That plan is the harm."
    exit 0
  fi
  fail "bulkread" "a record read that failed for one key produced a plan that proposes creating that resource: $T_OUT"
fi
[ "${BREAK:-0}" = "1" ] && fail "bulkread" "the binary built to drop a failed key still produced a true plan, so the break did not take and this control proves nothing: $T_OUT"
[ "$T_RC" = "0" ] || grep -q "$(basename "$VICTIM")" <<< "$(flat <<< "$T_OUT")" \
  || fail "bulkread" "the plan failed without naming the record it could not read: $T_OUT"
grep -E 'No changes\.|Error:' <<< "$T_OUT" | head -1 | evidence
proof "no resource was proposed for creation. The read was not short."

step "4. the same GET fails every time"
explain \
  "Now the record cannot be read at all. There is no true plan to be had," \
  "so the run must refuse and name the record, never plan around it."
: > "$SMOKE_WORK/proxy.log"
echo "$VICTIM -1" > "$SMOKE_WORK/fail"
rm -f "$SMOKE_WORK/est/.terraform/choudoufu-cache.tfstate"
cmd "choudoufu plan   # $VICTIM answers 500 every time"
P_OUT="$(run plan -input=false -no-color 2>&1)" && fail "bulkread" "the plan SUCCEEDED with a record that cannot be read: $P_OUT"
grep -qE 'Plan: [1-9][0-9]* to add' <<< "$P_OUT" && fail "bulkread" "the run failed but still rendered a plan that creates the unreadable resource: $P_OUT"
grep -q "$(basename "$VICTIM")" <<< "$(flat <<< "$P_OUT")" || fail "bulkread" "the refusal does not name the record it could not read: $P_OUT"
grep -E 'Error:' <<< "$P_OUT" | head -1 | evidence
proof "refused, naming the record. An unreadable record is not an absent one."

step "5. teardown"
rm -f "$SMOKE_WORK/fail"
cmd "choudoufu apply -destroy -auto-approve"
run apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 || fail "bulkread" "teardown failed"
proof "gone."

echo "  What you watched: $N records read through a proxy, one GET failed once"
echo "  and then always, and no plan at any point proposing to create a"
echo "  resource whose record was merely unreadable."

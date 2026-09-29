#!/usr/bin/env python3
"""A fault-injecting proxy for the smoke scenarios that need one.

    python3 s3proxy.py <upstream-port> <work-dir>

It forwards every request to the emulator on localhost:<upstream-port>
untouched, writes the port it chose to <work-dir>/proxy.port, and logs one
line per request to <work-dir>/proxy.log ("METHOD path status", and for a
PUT the conditional-write header it carried as a fourth field). Two control
files in <work-dir> change what it does, and a scenario drives it by writing
them. Nothing in the emulator or in the cloud can be corrupted into either
behaviour, which is why this exists.

fail     "<substring> <count>" or "<substring> <count> 404"
         A GetObject whose path contains <substring> is answered 500, <count>
         times (-1 is forever). ListObjectsV2 is never failed. Claim 31.

         With the third field "404" the answer is S3's own 404 NoSuchKey
         instead, which is a different fault and not a milder one: a 500 is
         a read that FAILED, and a 404 is a read that SUCCEEDED and said no
         record is there. The listing is still forwarded, so it goes on
         naming the key. That is a store contradicting itself about one
         record, which is GitHub issue #1355's route, and every GET of the
         key gets it - the bulk read's, its second look, and the per-key
         read the run falls back to. Claim 31, step 5.

hold     "<substring>"
         A PUT whose path contains <substring> is HELD: not forwarded, not
         answered. Each held PUT is logged to <work-dir>/held as
         "<seq> <marker>", where <seq> counts arrivals from 1 since the
         scenario last deleted <work-dir>/held, and <marker> is
         the first of <work-dir>/markers' whitespace-separated words found in
         the request body ("-" when none is), so a scenario can tell which of
         two racing writers arrived first.
count    "<substring>"
         A GET whose path contains <substring> is counted while it is in
         flight, and the highest number ever in flight at once is written to
         <work-dir>/inflight-max. Deleting that file resets the high-water
         mark, so a scenario measures one run at a time. Claim 31.

stall    "<substring> <seconds>"
         A GET whose path contains <substring> waits <seconds> before it is
         forwarded. Requests that are genuinely concurrent then overlap for
         long enough to be seen; without it a fan-out against a local
         emulator can finish each GET before the next begins and read as
         sequential. Claim 31.

release  "<seq> <seq> ..." or "drop"
         Held PUTs are forwarded one at a time in that order, each completing
         its round trip before the next starts, which is what makes a race
         deterministic: both writers' conditional PUTs are in hand before
         either is judged. "drop" closes every held connection without
         forwarding, which is a writer killed mid-write. Claim 32.
"""
import http.client
import http.server
import os
import socketserver
import sys
import threading
import time

upstream_port, work = int(sys.argv[1]), sys.argv[2]
lock = threading.Lock()
turn = threading.Condition()
released = set()

# The in-flight high-water mark (claim 31's "eight at a time"). A separate
# lock from `lock`, which the file-backed controls hold while they read and
# write, so counting a request in can never wait on one of those.
count_lock = threading.Lock()
in_flight = 0
in_flight_max = 0


def read(name):
    try:
        return open(os.path.join(work, name)).read().strip()
    except OSError:
        return ""


def log(line):
    with lock:
        open(os.path.join(work, "proxy.log"), "a").write(line + "\n")


FAULTS = {
    500: (b'<?xml version="1.0" encoding="UTF-8"?><Error><Code>InternalError</Code>'
          b"<Message>injected by the smoke proxy</Message></Error>"),
    404: (b'<?xml version="1.0" encoding="UTF-8"?><Error><Code>NoSuchKey</Code>'
          b"<Message>The specified key does not exist. (injected by the smoke proxy)</Message></Error>"),
}


def should_fail(path, query):
    """The status to answer this GET with instead of forwarding it, or 0.

    0 and not False for "forward it", so the caller's test is one truth
    test and a status can never be mistaken for a flag.
    """
    if "list-type=2" in query:
        return 0
    with lock:
        parts = read("fail").split()
        if len(parts) not in (2, 3):
            return 0
        sub, count = parts[0], int(parts[1])
        status = int(parts[2]) if len(parts) == 3 else 500
        if sub not in path or count == 0 or status not in FAULTS:
            return 0
        if count > 0:
            open(os.path.join(work, "fail"), "w").write(" ".join([sub, str(count - 1)] + parts[2:]))
        return status


def count_enter(path):
    """Counts this GET in, if it is one the scenario asked to count.

    Returns True when it was counted, which is what obliges the caller to
    call count_leave. The high-water mark resets when the scenario deletes
    <work-dir>/inflight-max, so a mark left by an earlier measurement can
    never be read as this one's.
    """
    global in_flight, in_flight_max
    sub = read("count")
    if not sub or sub not in path:
        return False
    mark = os.path.join(work, "inflight-max")
    with count_lock:
        if not os.path.exists(mark):
            in_flight_max = 0
        in_flight += 1
        if in_flight > in_flight_max:
            in_flight_max = in_flight
            open(mark, "w").write(str(in_flight_max))
    return True


def count_leave():
    global in_flight
    with count_lock:
        in_flight -= 1


def stall(path):
    parts = read("stall").split()
    if len(parts) == 2 and parts[0] in path:
        time.sleep(float(parts[1]))


def precondition(headers):
    """The conditional-write header a PUT carried, for its log line.

    Every record store write is one conditional PutObject: If-None-Match: *
    to create a record, If-Match: <version> to update one. Claim 27's
    shared-store step (#1394) reads those off the wire instead of inferring
    them from the fact that the write landed, which is the only way to tell
    a conditional update from an unconditional overwrite that happened to
    be uncontended. Only PUT lines carry the field, so the GET and DELETE
    lines other scenarios grep are unchanged.
    """
    for name in ("If-Match", "If-None-Match"):
        value = headers.get(name)
        if value:
            return "%s: %s" % (name.lower(), value)
    return "no-precondition"


def hold(path, body):
    """Blocks until this PUT's turn.

    Returns 0 when the PUT is not one to hold, its arrival number (from 1)
    once it may be forwarded, and False when it was dropped. 0 and not True
    for "not held", because 1 == True in Python and the first held PUT would
    then read as "not held" to the caller and never be marked released.
    """
    sub = read("hold")
    if not sub or sub not in path:
        return 0
    with lock:
        # The arrival number is this PUT's line in <work-dir>/held, which the
        # scenario deletes between rounds, so every round counts from 1. A
        # counter kept here instead ran on across rounds: round two's PUTs
        # were 3 and 4, the scenario released "2 1", and both writers waited
        # for a turn that was never coming.
        seq = len(read("held").splitlines()) + 1
        if seq == 1:
            with turn:
                released.clear()
        marker = next((m for m in read("markers").split() if m.encode() in body), "-")
        open(os.path.join(work, "held"), "a").write("%d %s\n" % (seq, marker))
    while True:
        order = read("release")
        if order == "drop":
            return False
        want = [int(x) for x in order.split()] if order else []
        if seq in want:
            before = want[: want.index(seq)]
            with turn:
                if all(b in released for b in before):
                    return seq
        time.sleep(0.02)


class Handler(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *args):
        pass

    def relay(self):
        path, _, query = self.path.partition("?")
        body = self.rfile.read(int(self.headers.get("Content-Length") or 0))
        seq = 0
        if self.command == "PUT":
            seq = hold(path, body)
            if seq is False:
                log("PUT %s dropped" % self.path)
                self.close_connection = True
                self.connection.close()
                return
        counted = self.command == "GET" and "list-type=2" not in query and count_enter(path)
        fault = should_fail(path, query) if self.command == "GET" else 0
        if fault:
            payload = FAULTS[fault]
            self.send_response(fault)
            self.send_header("Content-Type", "application/xml")
            self.send_header("Content-Length", str(len(payload)))
            self.end_headers()
            self.wfile.write(payload)
            status = fault
        else:
            if self.command == "GET":
                stall(path)
            host = self.headers.get("Host", "localhost").rsplit(":", 1)[0]
            headers = {k: v for k, v in self.headers.items() if k.lower() not in ("host", "connection")}
            headers["Host"] = "%s:%d" % (host, upstream_port)
            conn = http.client.HTTPConnection("localhost", upstream_port, timeout=30)
            conn.request(self.command, self.path, body=body, headers=headers)
            resp = conn.getresponse()
            data = resp.read()
            status = resp.status
            conn.close()
            if seq:
                # Judged by the emulator. Only now may the next held PUT go.
                with turn:
                    released.add(seq)
            self.send_response(status)
            for k, v in resp.getheaders():
                if k.lower() not in ("transfer-encoding", "connection", "content-length"):
                    self.send_header(k, v)
            self.send_header("Content-Length", str(len(data)))
            self.end_headers()
            if self.command != "HEAD":
                self.wfile.write(data)
        if counted:
            count_leave()
        if self.command == "PUT":
            log("%s %s %d %s" % (self.command, self.path, status, precondition(self.headers)))
        else:
            log("%s %s %d" % (self.command, self.path, status))

    do_GET = do_PUT = do_DELETE = do_HEAD = do_POST = relay


class Server(socketserver.ThreadingMixIn, http.server.HTTPServer):
    daemon_threads = True

    # A client hanging up mid-response is a scenario doing its job (a fan-out
    # cancelling siblings, a writer killed on purpose), not a proxy fault.
    def handle_error(self, request, client_address):
        pass


srv = Server(("127.0.0.1", 0), Handler)
open(os.path.join(work, "proxy.port"), "w").write(str(srv.server_address[1]))
srv.serve_forever()

#!/usr/bin/env python3
"""A fault-injecting proxy for the smoke scenarios that need one.

    python3 s3proxy.py <upstream-port> <work-dir>

It forwards every request to the emulator on localhost:<upstream-port>
untouched, writes the port it chose to <work-dir>/proxy.port, and logs one
line per request to <work-dir>/proxy.log ("METHOD path status"). Two control
files in <work-dir> change what it does, and a scenario drives it by writing
them. Nothing in the emulator or in the cloud can be corrupted into either
behaviour, which is why this exists.

fail     "<substring> <count>"
         A GetObject whose path contains <substring> is answered 500, <count>
         times (-1 is forever). ListObjectsV2 is never failed. Claim 31.

hold     "<substring>"
         A PUT whose path contains <substring> is HELD: not forwarded, not
         answered. Each held PUT is logged to <work-dir>/held as
         "<seq> <marker>", where <seq> counts arrivals from 1 and <marker> is
         the first of <work-dir>/markers' whitespace-separated words found in
         the request body ("-" when none is), so a scenario can tell which of
         two racing writers arrived first.
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
held_seq = 0
turn = threading.Condition()
released = set()


def read(name):
    try:
        return open(os.path.join(work, name)).read().strip()
    except OSError:
        return ""


def log(line):
    with lock:
        open(os.path.join(work, "proxy.log"), "a").write(line + "\n")


def should_fail(path, query):
    if "list-type=2" in query:
        return False
    with lock:
        parts = read("fail").split()
        if len(parts) != 2:
            return False
        sub, count = parts[0], int(parts[1])
        if sub not in path or count == 0:
            return False
        if count > 0:
            open(os.path.join(work, "fail"), "w").write("%s %d" % (sub, count - 1))
        return True


def hold(path, body):
    """Blocks until this PUT's turn.

    Returns 0 when the PUT is not one to hold, its arrival number (from 1)
    once it may be forwarded, and False when it was dropped. 0 and not True
    for "not held", because 1 == True in Python and the first held PUT would
    then read as "not held" to the caller and never be marked released.
    """
    global held_seq
    sub = read("hold")
    if not sub or sub not in path:
        return 0
    with lock:
        held_seq += 1
        seq = held_seq
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
        if self.command == "GET" and should_fail(path, query):
            payload = (b'<?xml version="1.0" encoding="UTF-8"?><Error><Code>InternalError</Code>'
                       b"<Message>injected by the smoke proxy</Message></Error>")
            self.send_response(500)
            self.send_header("Content-Length", str(len(payload)))
            self.end_headers()
            self.wfile.write(payload)
            status = 500
        else:
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

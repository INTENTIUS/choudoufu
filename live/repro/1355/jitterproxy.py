#!/usr/bin/env python3
"""jitterproxy.py <listen-port> <upstream-port> <jitter-ms> <maxkeys> <log>

Forwards every request to the emulator. Each request waits a uniform random
0..jitter-ms before it is forwarded AND before its answer is returned, so
concurrent GETs complete out of order. With maxkeys > 0, every ListObjectsV2
has max-keys rewritten to that value, which forces pagination with
continuation tokens on a two-record namespace. Counts go to <log>.
"""
import http.client
import http.server
import random
import socketserver
import sys
import threading
import time
import urllib.parse

listen, upstream, jitter, maxkeys, logpath = int(sys.argv[1]), int(sys.argv[2]), int(sys.argv[3]), int(sys.argv[4]), sys.argv[5]
lock = threading.Lock()
counts = {}


def note(k):
    with lock:
        counts[k] = counts.get(k, 0) + 1


class H(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *a):
        pass

    def _do(self):
        path = self.path
        u = urllib.parse.urlsplit(path)
        q = urllib.parse.parse_qsl(u.query, keep_blank_values=True)
        if maxkeys > 0 and any(k == "list-type" for k, _ in q):
            q = [(k, v) for k, v in q if k != "max-keys"] + [("max-keys", str(maxkeys))]
            path = urllib.parse.urlunsplit(("", "", u.path, urllib.parse.urlencode(q), ""))
            note("list-rewritten")
        note(self.command)
        if jitter > 0:
            time.sleep(random.uniform(0, jitter) / 1000.0)
        n = int(self.headers.get("Content-Length") or 0)
        body = self.rfile.read(n) if n else None
        hdrs = {k: v for k, v in self.headers.items() if k.lower() not in ("host", "connection")}
        host = self.headers.get("Host", "localhost")
        hdrs["Host"] = host.rsplit(":", 1)[0] + ":%d" % upstream
        c = http.client.HTTPConnection("localhost", upstream, timeout=60)
        c.request(self.command, path, body=body, headers=hdrs)
        r = c.getresponse()
        data = r.read()
        if jitter > 0:
            time.sleep(random.uniform(0, jitter) / 1000.0)
        self.send_response(r.status)
        for k, v in r.getheaders():
            if k.lower() in ("transfer-encoding", "connection", "content-length"):
                continue
            self.send_header(k, v)
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        if self.command != "HEAD":
            self.wfile.write(data)
        c.close()

    do_GET = do_PUT = do_POST = do_DELETE = do_HEAD = _do


class S(socketserver.ThreadingMixIn, http.server.HTTPServer):
    daemon_threads = True
    allow_reuse_address = True


def dump():
    while True:
        time.sleep(2)
        with lock:
            open(logpath, "w").write(repr(counts) + "\n")


threading.Thread(target=dump, daemon=True).start()
S(("127.0.0.1", listen), H).serve_forever()

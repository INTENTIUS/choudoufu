#!/usr/bin/env python3
"""A fault-injecting proxy in front of a kind cluster's API server.

    python3 k8sproxy.py <upstream-url> <work-dir>

It is live/smoke/s3proxy.py for the Kubernetes record store: claim 31 on the
cluster (k8s-records-in-the-cluster, step 12) needs one page of a paged LIST
to fail from OUTSIDE the binary, and nothing in a kind cluster can be made to
expire a continue token on cue. The API server's watch cache keeps a token
good for minutes, and the only way to hand a run the 410 it answers with
after that is to put it on the wire ourselves.

It re-terminates TLS. The kind API server speaks TLS and authenticates the
admin by client certificate, so a plain TCP relay could not read a request
to decide whether to fail it. This proxy serves TLS with its own self-signed
certificate (<work-dir>/proxy.crt and proxy.key, written by the scenario
with openssl), and the kubeconfig the run is handed points its cluster
`server:` at this proxy with `insecure-skip-tls-verify: true`. Upstream it
speaks TLS to the real API server, verifying it against
<work-dir>/upstream-ca.crt and authenticating with the admin's own
<work-dir>/upstream-client.crt and upstream-client.key, all three taken out
of the kind kubeconfig. Whatever identity the client presents is therefore
never what reaches the cluster: every request is forwarded as the admin.
That is fine for the one scenario this serves, whose measured runs are the
admin's already, and it is why this file is not a general tool.

It writes the port it chose to <work-dir>/proxy.port and logs one line per
request to <work-dir>/proxy.log ("METHOD path?query status"), so a scenario
can read back how many LISTs went by, how many carried a continue token, and
how many were answered by the fault rather than the cluster.

Two control files in <work-dir> change what it does, and a scenario drives
them by writing the files:

expire   "<count>"
         A GET of a `/secrets` collection whose query carries a continue
         token - the second and later pages of a paged LIST - is answered
         410 Gone with the API server's own Status body (reason Expired),
         <count> times (-1 is forever). The first page is never failed, so
         what the run gets is exactly what an expired token gets it: a good
         first page and a refused second one. Claim 31.

skip     "<count>"
         The first <count> later-page requests that `expire` would answer
         are relayed to the cluster instead, and only then does `expire`
         start answering. A run lists the records namespace more than once:
         once when the store opens, to read its sentinel back, and again for
         the bulk read the plan is built on. skip 1 lets the first through,
         so the 410 lands on the second, which is the read claim 31 is
         about; with no skip it lands on the first.
"""
import http.client
import http.server
import os
import socketserver
import ssl
import sys
import threading
from urllib.parse import urlsplit

upstream, work = urlsplit(sys.argv[1]), sys.argv[2]
lock = threading.Lock()

# The API server's own answer to a continue token that has outlived its watch
# cache, byte for byte the shape client-go decodes into a StatusError with
# reason Expired and code 410. The wording is the one
# internal/live/staterecord/kubernetes_missing_write_live_test.go puts on the
# wire for the same fault, and the one the store's error text is held to.
EXPIRED = (
    b'{"kind":"Status","apiVersion":"v1","metadata":{},"status":"Failure",'
    b'"message":"The provided continue parameter is too old to display a consistent list result. '
    b'If the list result is not expected to change within the watch cache, use RV=0 or set '
    b'resourceVersionMatch to Exact. Otherwise, either re-list from the start, or use a newer '
    b'continue parameter. (injected by the smoke proxy)",'
    b'"reason":"Expired","code":410}'
)


def read(name):
    try:
        return open(os.path.join(work, name)).read().strip()
    except OSError:
        return ""


def log(line):
    with lock:
        open(os.path.join(work, "proxy.log"), "a").write(line + "\n")


def should_expire(method, path, query):
    """True when this request is a later page of a Secrets LIST and the
    scenario has asked for later pages to be answered 410."""
    if method != "GET" or "/secrets" not in path or "continue=" not in query:
        return False
    with lock:
        raw = read("expire")
        if not raw:
            return False
        count = int(raw)
        if count == 0:
            return False
        skip = int(read("skip") or "0")
        if skip > 0:
            open(os.path.join(work, "skip"), "w").write(str(skip - 1))
            return False
        if count > 0:
            open(os.path.join(work, "expire"), "w").write(str(count - 1))
        return True


upstream_ctx = ssl.create_default_context(cafile=os.path.join(work, "upstream-ca.crt"))
upstream_ctx.load_cert_chain(os.path.join(work, "upstream-client.crt"), os.path.join(work, "upstream-client.key"))


class Handler(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *args):
        pass

    def relay(self):
        path, _, query = self.path.partition("?")
        body = self.rfile.read(int(self.headers.get("Content-Length") or 0))
        if should_expire(self.command, path, query):
            status = 410
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(EXPIRED)))
            self.end_headers()
            self.wfile.write(EXPIRED)
        else:
            headers = {k: v for k, v in self.headers.items() if k.lower() not in ("host", "connection")}
            headers["Host"] = upstream.netloc
            conn = http.client.HTTPSConnection(upstream.hostname, upstream.port, context=upstream_ctx, timeout=60)
            conn.request(self.command, self.path, body=body, headers=headers)
            resp = conn.getresponse()
            data = resp.read()
            status = resp.status
            conn.close()
            self.send_response(status)
            for k, v in resp.getheaders():
                if k.lower() not in ("transfer-encoding", "connection", "content-length"):
                    self.send_header(k, v)
            self.send_header("Content-Length", str(len(data)))
            self.end_headers()
            if self.command != "HEAD":
                self.wfile.write(data)
        log("%s %s %d" % (self.command, self.path, status))

    do_GET = do_PUT = do_DELETE = do_HEAD = do_POST = do_PATCH = relay


class Server(socketserver.ThreadingMixIn, http.server.HTTPServer):
    daemon_threads = True

    # A client hanging up mid-response, or a run cancelling the requests it
    # had in flight once one of them failed, is a scenario doing its job.
    def handle_error(self, request, client_address):
        pass


srv = Server(("127.0.0.1", 0), Handler)
serve_ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
serve_ctx.load_cert_chain(os.path.join(work, "proxy.crt"), os.path.join(work, "proxy.key"))
srv.socket = serve_ctx.wrap_socket(srv.socket, server_side=True)
open(os.path.join(work, "proxy.port"), "w").write(str(srv.server_address[1]))
srv.serve_forever()

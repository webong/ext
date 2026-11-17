"""The HTTPS origin of the download test: serves the routes of
lib/plugin/testdata/v1/download-cases.json with a certificate made for the test,
beside a plain HTTP server that must never receive a request.

Usage: python3 -I origin.py <download-cases.json> <scratch directory>

It writes the certificate to <scratch>/origin.pem, prints one JSON line
{"origin": "127.0.0.1:<port>", "plain": "127.0.0.1:<port>"} and serves until it is
killed. The headers of the last request the origin received are in
<scratch>/landing.json (names in lower case); a request to the plain server creates
<scratch>/plain-hit. /archives/<name> serves that file of the fixtures, for an
install through the download.
"""
import http.server
import json
import os
import ssl
import subprocess
import sys
import threading

cases_path, scratch = sys.argv[1], sys.argv[2]
routes = json.load(open(cases_path))["routes"]
cert = os.path.join(scratch, "origin.pem")
key = os.path.join(scratch, "origin.key")
subprocess.run(
    ["openssl", "req", "-x509", "-newkey", "ec", "-pkeyopt", "ec_paramgen_curve:prime256v1",
     "-nodes", "-days", "1", "-subj", "/CN=ext download test", "-addext", "subjectAltName=IP:127.0.0.1",
     "-addext", "basicConstraints=critical,CA:TRUE", "-keyout", key, "-out", cert],
    check=True, capture_output=True)


class Quiet(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *args):
        pass


class Origin(Quiet):
    def do_GET(self):
        landing = {name.lower(): value for name, value in self.headers.items()}
        with open(os.path.join(scratch, "landing.json.tmp"), "w") as out:
            json.dump(landing, out)
        os.replace(os.path.join(scratch, "landing.json.tmp"), os.path.join(scratch, "landing.json"))
        path = self.path.split("?")[0]
        route = routes.get(path)
        if route is None and path.startswith("/archives/") and "/" not in path[len("/archives/"):]:
            archive = os.path.join(os.path.dirname(cases_path), path[1:])
            if os.path.isfile(archive):
                route = {"bytes": open(archive, "rb").read()}
        if route is None:
            route = {"status": 404, "body": "no route"}
        if "redirect" in route:
            location = route["redirect"].replace("{origin}", origin).replace("{plain}", plain)
            self.send_response(302)
            self.send_header("Location", location)
            self.send_header("Content-Length", "0")
            self.send_header("Connection", "close")
            self.end_headers()
            return
        if "bytes" in route:
            body = route["bytes"]
        elif "body" in route:
            body = route["body"].encode()
        else:
            body = b"x" * route.get("bodyBytes", 0)
        self.send_response(route.get("status", 200))
        self.send_header("Connection", "close")
        if route.get("chunked"):
            self.send_header("Transfer-Encoding", "chunked")
            self.end_headers()
            for start in range(0, len(body), 512):
                piece = body[start:start + 512]
                self.wfile.write(b"%x\r\n%s\r\n" % (len(piece), piece))
            self.wfile.write(b"0\r\n\r\n")
        else:
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)


class Plain(Quiet):
    def do_GET(self):
        open(os.path.join(scratch, "plain-hit"), "w").close()
        body = b"payload"
        self.send_response(200)
        self.send_header("Content-Length", str(len(body)))
        self.send_header("Connection", "close")
        self.end_headers()
        self.wfile.write(body)


secure = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Origin)
context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
context.load_cert_chain(cert, key)
secure.socket = context.wrap_socket(secure.socket, server_side=True)
insecure = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Plain)
origin = "127.0.0.1:%d" % secure.server_address[1]
plain = "127.0.0.1:%d" % insecure.server_address[1]
threading.Thread(target=insecure.serve_forever, daemon=True).start()
print(json.dumps({"origin": origin, "plain": plain}), flush=True)
secure.serve_forever()

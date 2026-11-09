#!/usr/bin/env python3
"""CA-verified HTTP seam checks for a running isolated Caddy/MediaMTX stack."""
import base64
import os
import ssl
import sys
from urllib.error import HTTPError
from urllib.request import Request, urlopen


base = os.environ.get("WBB_DEVICE_URL")
ca = os.environ.get("WBB_DEVICE_CA")
user = os.environ.get("WBB_DEVICE_USER")
password = os.environ.get("WBB_DEVICE_PASSWORD")
if not all((base, ca, user, password)):
    raise SystemExit("Set WBB_DEVICE_URL/CA/USER/PASSWORD for the isolated stack")
context = ssl.create_default_context(cafile=ca)
auth = "Basic " + base64.b64encode(f"{user}:{password}".encode()).decode()
results = []


def request(path, method="GET", credential=None, origin=None, body=None):
    headers = {}
    if credential:
        headers["Authorization"] = credential
    if origin is not None:
        headers["Origin"] = origin
    if body is not None:
        headers["Content-Type"] = "application/sdp"
    req = Request(base + path, data=body, headers=headers, method=method)
    try:
        with urlopen(req, timeout=5, context=context) as response:
            return response.status, response.headers
    except HTTPError as exc:
        return exc.code, exc.headers


def check(label, ok):
    print(("PASS" if ok else "FAIL") + " " + label)
    results.append(ok)


for path in ("/__media-check/", "/__media-check/app.js", "/__media-check/config.js",
             "/__media-check/vendor/hls.min.js", "/__media-check/hls/spike/720p/index.m3u8",
             "/__media-check/hls/spike/720p/segment.mp4", "/__media-check/whip/spike/source/whip"):
    check("unauth GET " + path.rsplit("/", 1)[-1], request(path)[0] == 401)
for method in ("POST", "PATCH", "DELETE"):
    check("unauth WHIP " + method,
          request("/__media-check/whip/spike/source/whip", method,
                  origin=base, body=b"invalid" if method == "POST" else None)[0] == 401)
check("wrong credential", request("/__media-check/", credential="Basic Zm9vOmJhcg==")[0] == 401)
check("authenticated UI", request("/__media-check/", credential=auth)[0] == 200)
whip = "/__media-check/whip/spike/source/whip"
check("wrong Origin before upstream", request(whip, "POST", auth, "https://wrong.example", b"invalid")[0] == 403)
check("missing Origin before upstream", request(whip, "PATCH", auth)[0] == 403)
check("valid Origin reaches real MediaMTX", request(whip, "POST", auth, base, b"invalid")[0] == 400)
check("private API unavailable", request("/v3/paths/list", credential=auth)[0] == 404)
status, headers = request("/__media-check/hls/spike/720p/index.m3u8", credential=auth)
if status == 404:
    print("SKIP HLS redirect (no active publisher; verified by device-flow check)")
else:
    check("HLS redirect keeps authenticated prefix", status in (200, 302) and
          (status == 200 or headers.get("Location", "").startswith("/__media-check/hls/")))
print(f"{sum(results)}/{len(results)} boundary checks")
if not all(results):
    sys.exit(1)

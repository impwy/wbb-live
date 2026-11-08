#!/usr/bin/env python3
"""Error-injection selftest for verify-termination.py.

Proves PASS/FAIL accuracy by stubbing `docker` (via PATH) and the HTTP
endpoints (via a local stub server + WBB_TERM_* env overrides):

  1. exec-failure during before            -> FAIL
  2. happy before                          -> PASS (state published)
  3. exec-failure during after convergence -> FAIL
  4. original encoder never dies           -> FAIL (convergence timeout)
  5. happy after                           -> PASS (>=3 samples, final at t>=window)
  6. 404 -> 200 reappearance               -> FAIL
  7. part progression after convergence    -> FAIL

No real docker/GPU/network is used.
"""
import json
import os
import shutil
import stat
import subprocess
import sys
import tempfile
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

HERE = os.path.dirname(os.path.abspath(__file__))

results = []
def check(name, ok, detail=""):
    results.append(ok)
    print(f"{'PASS' if ok else 'FAIL'}  {name}" + (f" — {detail}" if detail else ""))

# ---------------------------------------------------------------- stub docker
DOCKER_STUB = r"""#!/bin/sh
# Controlled by $STUB_DIR/env (written by the harness before each scenario).
. "$STUB_DIR/env"
if [ "$STUB_FAIL_EXEC" = "1" ]; then exit 17; fi
case "$*" in *"/proc/"*"/stat"*) if [ "$STUB_FAIL_STAT" = "1" ]; then exit 18; fi ;; esac
case "$*" in
  *"ps -q transcoder"*) echo "$STUB_CT" ;;
  *pidof*ffmpeg*)
    # `docker compose exec -T transcoder sh -c 'pidof ffmpeg ...'`
    case "$2" in *[[:space:]]*) ;; esac
    echo "$STUB_PIDS" ;;
  *"/proc/"*"/stat"*)
    cmd="$*"
    pid=$(echo "$cmd" | sed -n 's/.*\/proc\/\([0-9]*\)\/stat.*/\1/p')
    eval "result=\$STUB_STAT_$pid"
    if [ -n "$result" ]; then echo "$result"; else echo GONE; fi ;;
  *) echo "stub docker: unexpected: $*" >&2; exit 9 ;;
esac
"""

class StubHandler(BaseHTTPRequestHandler):
    # Behaviour selected by harness via server attributes.
    paths_mode = "ready"      # ready | inactive | error
    playlist_mode = "fixed"   # fixed | progressing | gone | reappear
    _hits = 0

    def log_message(self, *a):
        pass

    def _playlist(self):
        StubHandler._hits += 1
        mode = StubHandler.playlist_mode
        if mode == "gone":
            self.send_response(404); self.end_headers(); return
        if mode == "reappear":
            if StubHandler._hits % 3 == 2:
                self.send_response(404); self.end_headers(); return
        msn = 7
        part = "seg7.part0.m4s"
        if mode == "progressing":
            msn = 7 + StubHandler._hits
            part = f"seg{msn}.part{StubHandler._hits}.m4s"
        body = (
            "#EXTM3U\n#EXT-X-VERSION:10\n"
            "#EXT-X-SERVER-CONTROL:CAN-BLOCK-RELOAD=YES\n"
            f"#EXT-X-PART-INF:PART-TARGET=0.2\n#EXT-X-MEDIA-SEQUENCE:{msn}\n"
            f'#EXT-X-PART:DURATION=0.2,URI="{part}"\n#EXTINF:2.0,\nseg{msn}.mp4\n'
        ).encode()
        self.send_response(200)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        if self.path.startswith("/v3/paths/list"):
            if StubHandler.paths_mode == "error":
                self.send_response(500); self.end_headers(); return
            if StubHandler.paths_mode == "ready":
                item = {"name": "", "ready": True, "source": {"type": "webRTCSession"}}
                items = [dict(item, name="spike/source"), dict(item, name="spike/720p")]
            else:
                items = [{"name": n, "ready": False, "source": None} for n in ("spike/source", "spike/720p")]
            body = json.dumps({"itemCount": len(items), "items": items}).encode()
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
        elif "index.m3u8" in self.path:
            # master playlist pointing at the variant below
            body = ("#EXTM3U\n#EXT-X-VERSION:10\n"
                    '#EXT-X-STREAM-INF:BANDWIDTH=1,CODECS="avc1.4d401f,mp4a.40.2"\n'
                    "video1_stream.m3u8?session=stub\n").encode()
            self.send_response(200)
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
        elif "_stream" in self.path:
            self._playlist()
        else:
            self.send_response(404); self.end_headers()


def run_scenario(tmp, name, mode, args, expect_rc_zero, timeout=30):
    """mode: dict of stub env + server behaviour. Returns (rc, output)."""
    env = dict(os.environ)
    env.update({
        "PATH": tmp + "/bin:" + env["PATH"],
        "STUB_DIR": tmp,
        "WBB_TERM_API_BASE": f"http://127.0.0.1:{HTTP_PORT}",
        "WBB_TERM_HLS_BASE": f"http://127.0.0.1:{HTTP_PORT}",
        "WBB_TERM_CONV_LIMIT": "3",
        "WBB_TERM_WINDOW": "1",
        "WBB_TERM_INTERVAL": "0.3",
        "WBB_TERM_STATE": os.path.join(tmp, "state"),
    })
    env.update({k: str(v) for k, v in mode.get("env", {}).items()})
    StubHandler.paths_mode = mode.get("paths", "ready")
    StubHandler.playlist_mode = mode.get("playlist", "fixed")
    StubHandler._hits = 0
    proc = subprocess.run(
        [sys.executable, os.path.join(HERE, "verify-termination.py"), args],
        capture_output=True, text=True, env=env, timeout=timeout,
    )
    ok = (proc.returncode == 0) == expect_rc_zero
    check(name, ok, f"rc={proc.returncode} out={proc.stdout.strip()[-80:]} err={proc.stderr.strip()[-120:]}")
    return proc


def main():
    global HTTP_PORT
    srv = ThreadingHTTPServer(("127.0.0.1", 0), StubHandler)
    HTTP_PORT = srv.server_address[1]
    threading.Thread(target=srv.serve_forever, daemon=True).start()

    tmp = tempfile.mkdtemp(prefix="wbb-term-selftest-")
    state = os.path.join(tmp, "state")
    os.makedirs(tmp + "/bin")
    stub = tmp + "/bin/docker"
    with open(stub, "w") as f:
        f.write(DOCKER_STUB)
    os.chmod(stub, os.stat(stub).st_mode | stat.S_IEXEC)

    def set_env(**kv):
        with open(tmp + "/env", "w") as f:
            for k, v in kv.items():
                f.write(f'{k}="{v}"\n')

    happy_env = dict(STUB_FAIL_EXEC=0, STUB_FAIL_STAT=0, STUB_CT="ct123", STUB_PIDS="712",
                     STUB_STAT_712="51634677")
    # 1. exec failure during before -> FAIL (no state published).
    set_env(**{**happy_env, "STUB_FAIL_EXEC": 1})
    run_scenario(tmp, "T1 exec-fail in before fails", {}, "before", expect_rc_zero=False)
    check("T1 state not published", not os.path.exists(state))

    # 2. happy before -> PASS.
    set_env(**happy_env)
    StubHandler.playlist_mode = "progressing"
    run_scenario(tmp, "T2 happy before passes", {"playlist": "progressing"}, "before", expect_rc_zero=True)
    check("T2 state published", os.path.exists(state) and os.path.exists(state + ".ct"))

    # 3. exec failure during after -> FAIL.
    set_env(**{**happy_env, "STUB_FAIL_EXEC": 1})
    run_scenario(tmp, "T3 exec-fail in after fails", {}, "after", expect_rc_zero=False)

    # compose ps succeeds; the original PID's direct /proc read fails.
    set_env(**{**happy_env, "STUB_FAIL_STAT": 1})
    run_scenario(tmp, "T3b original PID stat exec-fail fails", {}, "after", expect_rc_zero=False)

    # 4. original encoder never dies -> convergence timeout FAIL.
    set_env(**happy_env)
    run_scenario(tmp, "T4 original alive -> timeout fail", {"paths": "inactive", "playlist": "fixed"}, "after", expect_rc_zero=False)

    # 5. happy after (original gone) -> PASS with >=3 samples incl. final.
    set_env(**{**happy_env, "STUB_PIDS": "999", "STUB_STAT_712": ""})  # pid 712 -> GONE
    proc = run_scenario(tmp, "T5 happy after passes", {"paths": "inactive", "playlist": "fixed"}, "after", expect_rc_zero=True)
    check("T5 final-sample evidence", "samples=" in proc.stdout)

    # 6. 404 -> 200 reappearance -> FAIL.
    set_env(**{**happy_env, "STUB_PIDS": "999", "STUB_STAT_712": ""})
    run_scenario(tmp, "T6 404->200 reappearance fails", {"paths": "inactive", "playlist": "reappear"}, "after", expect_rc_zero=False)

    # 7. part progression after convergence -> FAIL.
    set_env(**{**happy_env, "STUB_PIDS": "999", "STUB_STAT_712": ""})
    run_scenario(tmp, "T7 post-stop progression fails", {"paths": "inactive", "playlist": "progressing"}, "after", expect_rc_zero=False)

    # 8. API error during after -> FAIL.
    set_env(**{**happy_env, "STUB_PIDS": "999", "STUB_STAT_712": ""})
    run_scenario(tmp, "T8 API error fails", {"paths": "error", "playlist": "fixed"}, "after", expect_rc_zero=False)

    shutil.rmtree(tmp, ignore_errors=True)
    srv.shutdown()
    passed = sum(results)
    print(f"\n{passed}/{len(results)} 통과")
    sys.exit(0 if passed == len(results) else 1)


if __name__ == "__main__":
    main()

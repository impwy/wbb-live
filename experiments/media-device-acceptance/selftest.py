#!/usr/bin/env python3
"""Local failure injection; no real stack or credentials are used."""
import json
import os
from pathlib import Path
import shutil
import signal
import socket
import subprocess
import tempfile
import time

HERE = Path(__file__).resolve().parent
FAKE_HASH = "$2a$14$" + "A" * 53


def free_port():
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


HTTPS_PORT = free_port()
ICE_PORT = free_port()


def setup(base):
    project = base / "media-device-acceptance"
    project.mkdir()
    for name in ("settings.py", "common.sh", "lifecycle.py", "Caddyfile", "docker-compose.yml", "start.sh", "preflight.sh", "stop.sh", "mediamtx.yml"):
        shutil.copy2(HERE / name, project / name)
    (base / "media-single-rendition" / "web" / "vendor").mkdir(parents=True)
    web = base / "media-single-rendition" / "web"
    (web / "index.html").write_text('<script src="app.js"></script>')
    (web / "app.js").write_text("window.test = true;")
    (web / "vendor" / "empty.js").write_text("")
    env = {
        "WBB_MEDIA_AUTH_USER": "test-user",
        "WBB_MEDIA_AUTH_HASH": f"'{FAKE_HASH}'",
        "WBB_MEDIA_HOSTNAME": "127.0.0.1",
        "WBB_MEDIA_HTTPS_PORT": str(HTTPS_PORT),
        "WBB_MEDIA_ORIGIN": f"https://127.0.0.1:{HTTPS_PORT}",
        "WBB_MEDIA_BIND_IP": "127.0.0.1",
        "WBB_MEDIA_ICE_HOST": "127.0.0.1",
        "WBB_MEDIA_ICE_PORT": str(ICE_PORT),
        "WBB_MEDIA_ENCODER": "cpu",
    }
    (project / ".env").write_text("".join(f"{k}={v}\n" for k, v in env.items()))
    return project


def run(project, args, env=None):
    return subprocess.run(args, cwd=project, env=env, text=True, capture_output=True, timeout=12)


def check(name, condition):
    print(("PASS" if condition else "FAIL") + " " + name)
    if not condition:
        raise SystemExit(1)


with tempfile.TemporaryDirectory(prefix="wbb-device-selftest-") as tmp:
    base = Path(tmp)
    project = setup(base)
    hostile = dict(os.environ, COMPOSE_FILE="/tmp/other-project.yml", COMPOSE_PROJECT_NAME="other-project",
                   WBB_MEDIA_AUTH_HASH="inherited-wrong-hash")
    checked = run(project, ["python3", "settings.py", "check"])
    check("strict settings accept literal bcrypt", checked.returncode == 0)
    configured = run(project, ["sh", "-c", ". ./common.sh; compose config --format json"], hostile)
    config = json.loads(configured.stdout) if configured.returncode == 0 else {}
    caddy = config.get("services", {}).get("caddy", {})
    if configured.returncode != 0:
        print("compose config error:", configured.stderr[:300])
    else:
        observed_hash = caddy.get("environment", {}).get("WBB_MEDIA_AUTH_HASH", "")
        print("compose config observed:", config.get("name"), "caddy env type", type(caddy.get("environment")).__name__,
              "hash matched", observed_hash.replace("$$", "$") == FAKE_HASH)
    check("inherited Compose project/file and bcrypt cannot override", configured.returncode == 0 and
          config.get("name") == "wbb-media-acceptance" and
          caddy.get("environment", {}).get("WBB_MEDIA_AUTH_HASH", "").replace("$$", "$") == FAKE_HASH)
    original = (project / ".env").read_text()
    for label, altered in (
        ("unquoted bcrypt", original.replace(f"'{FAKE_HASH}'", FAKE_HASH)),
        ("missing origin", "\n".join(line for line in original.splitlines() if not line.startswith("WBB_MEDIA_ORIGIN="))),
        ("malformed origin", original.replace(f"https://127.0.0.1:{HTTPS_PORT}", "https://evil.example")),
        ("wildcard bind", original.replace("WBB_MEDIA_BIND_IP=127.0.0.1", "WBB_MEDIA_BIND_IP=0.0.0.0")),
        ("ICE label over 63", original.replace("WBB_MEDIA_ICE_HOST=127.0.0.1", "WBB_MEDIA_ICE_HOST=" + "a" * 64 + ".test")),
        ("HTTPS leading hyphen", original.replace("WBB_MEDIA_HOSTNAME=127.0.0.1", "WBB_MEDIA_HOSTNAME=-bad.test")),
        ("ICE trailing hyphen", original.replace("WBB_MEDIA_ICE_HOST=127.0.0.1", "WBB_MEDIA_ICE_HOST=bad-.test")),
        ("invalid numeric IP", original.replace("WBB_MEDIA_ICE_HOST=127.0.0.1", "WBB_MEDIA_ICE_HOST=999.999.999.999")),
    ):
        (project / ".env").write_text(altered)
        check(label + " rejected", run(project, ["python3", "settings.py", "check"]).returncode != 0)
    (project / ".env").write_text(original)

    public = original.replace("WBB_MEDIA_HOSTNAME=127.0.0.1", "WBB_MEDIA_HOSTNAME=live.wombatburrow.com")
    public = public.replace("WBB_MEDIA_ICE_HOST=127.0.0.1", "WBB_MEDIA_ICE_HOST=live.wombatburrow.com")
    public = public.replace(f"WBB_MEDIA_HTTPS_PORT={HTTPS_PORT}", "WBB_MEDIA_HTTPS_PORT=443")
    public = public.replace(f"https://127.0.0.1:{HTTPS_PORT}", "https://live.wombatburrow.com")
    (project / ".env").write_text(public)
    check("default HTTPS 443 omits port in Origin", run(project, ["python3", "settings.py", "check"]).returncode == 0)
    (project / ".env").write_text(public.replace("https://live.wombatburrow.com", "https://live.wombatburrow.com:443"))
    check("explicit 443 Origin rejected", run(project, ["python3", "settings.py", "check"]).returncode != 0)
    (project / ".env").write_text(original)

    # Stub Docker only. The actual public script interface is exercised.
    bindir = base / "bin"
    bindir.mkdir()
    log = base / "docker-calls"
    docker = bindir / "docker"
    docker.write_text('''#!/bin/sh
echo "$*" >> "$TEST_DOCKER_LOG"
case "$*" in
  *"config -q"*) [ "$TEST_HOLD" = config ] && sleep 20 ;;
  *" ps -aq"*) [ "$TEST_HOLD" = ps ] && sleep 20 ;;
  "run "*) [ "$TEST_HOLD" = validate ] && sleep 20 ;;
  *" up -d"*) [ "$TEST_HOLD" = up ] && sleep 20 ;;
esac
exit 0
''')
    docker.chmod(0o755)
    stub_env = dict(hostile, PATH=str(bindir) + os.pathsep + os.environ["PATH"], TEST_DOCKER_LOG=str(log))
    for hold, reached, forbidden in (
        ("config", "config -q", " ps -aq"),
        ("ps", " ps -aq", "run --rm"),
        ("validate", "run --rm", " up -d"),
        ("up", " up -d", " exec "),
    ):
        log.write_text("")
        proc = subprocess.Popen(["sh", "./start.sh"], cwd=project,
                                env=dict(stub_env, TEST_HOLD=hold),
                                stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        for _ in range(200):
            if reached in log.read_text():
                break
            time.sleep(0.02)
        proc.send_signal(signal.SIGTERM)
        stdout, stderr = proc.communicate(timeout=10)
        calls = log.read_text()
        if not (proc.returncode == 143 and reached in calls and forbidden not in calls and
                "Ready:" not in stdout and (" down --remove-orphans" in calls) == (hold == "up") and
                ("rm -f wbb-media-acceptance-validate-" in calls) == (hold == "validate" or hold == "up")):
            print("signal diagnostic", hold, proc.returncode, repr(calls), repr(stderr[:200]))
        check(f"TERM during {hold} exits 143 and stops descendants",
              proc.returncode == 143 and reached in calls and forbidden not in calls and
              "Ready:" not in stdout and (" down --remove-orphans" in calls) == (hold == "up") and
              ("rm -f wbb-media-acceptance-validate-" in calls) == (hold == "validate" or hold == "up"))

    timed = run(project, ["python3", "-c", "import lifecycle; lifecycle.command('probe', ['sleep', '20'], 1)"], stub_env)
    check("child timeout fails", timed.returncode != 0 and "timed out" in timed.stderr)

    marker = base / "late-grandchild-marker"
    fork_command = f"(sleep 0.6; printf late > '{marker}') & exit 0"
    fork_probe = (
        "import lifecycle; lifecycle.command('fork-probe', "
        f"['sh', '-c', {fork_command!r}], 0.15)"
    )
    forked = run(project, ["python3", "-c", fork_probe], stub_env)
    time.sleep(0.75)
    check("timeout kills grandchild after parent exit",
          forked.returncode != 0 and "timed out" in forked.stderr and not marker.exists())

    ready_marker = base / "ignore-term-ready"
    late_marker = base / "ignore-term-late"
    grandchild_code = (
        "import signal,time,pathlib; "
        "signal.signal(signal.SIGTERM, signal.SIG_IGN); "
        f"pathlib.Path({str(ready_marker)!r}).write_text('ready'); "
        f"time.sleep(0.6); pathlib.Path({str(late_marker)!r}).write_text('late')"
    )
    stubborn_command = (
        "import subprocess,sys,time,pathlib; "
        f"subprocess.Popen([sys.executable, '-c', {grandchild_code!r}], "
        "stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL); "
        f"ready=pathlib.Path({str(ready_marker)!r})\n"
        "while not ready.exists(): time.sleep(0.01)\n"
    )
    stubborn_probe = (
        "import lifecycle,sys; lifecycle.command('stubborn-probe', "
        f"[sys.executable, '-c', {stubborn_command!r}], 0.15)"
    )
    stubborn = run(project, ["python3", "-c", stubborn_probe], stub_env)
    time.sleep(0.75)
    if not (stubborn.returncode == 0 and ready_marker.exists() and not late_marker.exists()):
        print("stubborn diagnostic", stubborn.returncode, ready_marker.exists(), late_marker.exists(),
              stubborn.stderr[:160])
    check("SIGTERM-ignoring grandchild with closed pipes cannot outlive command",
          stubborn.returncode == 0 and ready_marker.exists() and not late_marker.exists())

    log.write_text("")
    stopped = run(project, ["sh", "./stop.sh"], dict(stub_env, TEST_HOLD=""))
    calls = log.read_text()
    check("stop ignores inherited Compose project/file", stopped.returncode == 0 and
          "-p wbb-media-acceptance" in calls and "-f " + str((project / "docker-compose.yml").resolve()) in calls)

#!/usr/bin/env python3
"""Bounded, signal-safe lifecycle for the dedicated device acceptance stack."""
import os
from pathlib import Path
import shutil
import signal
import socket
import subprocess
import sys
import tempfile
import time

from settings import load

HERE = Path(__file__).resolve().parent
PROJECT = "wbb-media-acceptance"
CADDY_IMAGE = "caddy:2.10-alpine@sha256:4c6e91c6ed0e2fa03efd5b44747b625fec79bc9cd06ac5235a779726618e530d"
VALIDATE_NAME = f"{PROJECT}-validate-{os.getpid()}"
COMPOSE = ["docker", "compose", "--project-directory", str(HERE),
           "--env-file", str(HERE / ".env"), "-p", PROJECT,
           "-f", str(HERE / "docker-compose.yml")]


class Interrupted(Exception):
    def __init__(self, number):
        self.number = number


def on_signal(number, _frame):
    raise Interrupted(number)


def clean_env(values=None):
    env = os.environ.copy()
    for key in list(env):
        if key.startswith("COMPOSE_") or key.startswith("WBB_MEDIA_"):
            env.pop(key)
    if values:
        env.update(values)
    return env


def terminate_child(child):
    # A Docker CLI/plugin can fork, close the direct child, and leave a
    # descendant in its process group. Reap the whole group on every exit.
    def group_exists():
        try:
            os.killpg(child.pid, 0)
        except ProcessLookupError:
            return False
        return True

    if not group_exists():
        child.communicate(timeout=2)
        return
    try:
        os.killpg(child.pid, signal.SIGTERM)
    except ProcessLookupError:
        child.communicate(timeout=2)
        return
    deadline = time.monotonic() + 0.25
    try:
        child.communicate(timeout=max(0.01, deadline - time.monotonic()))
    except subprocess.TimeoutExpired:
        pass
    # communicate() can return as soon as the direct child and pipes close,
    # while a descendant that ignores SIGTERM remains in this process group.
    while group_exists() and time.monotonic() < deadline:
        time.sleep(0.05)
    if group_exists():
        try:
            os.killpg(child.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
    child.communicate(timeout=2)


def command(label, argv, timeout, *, env=None, allowed=(0,)):
    child = subprocess.Popen(argv, cwd=HERE, env=env or clean_env(),
                             stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                             text=True, start_new_session=True)
    try:
        try:
            stdout, _stderr = child.communicate(timeout=timeout)
        except subprocess.TimeoutExpired as exc:
            raise RuntimeError(f"{label} timed out after {timeout}s") from exc
        if child.returncode not in allowed:
            raise RuntimeError(f"{label} failed (exit {child.returncode})")
        return stdout
    finally:
        terminate_child(child)


def compose(label, args, timeout, *, allowed=(0,)):
    return command(label, COMPOSE + args, timeout, allowed=allowed)


def remove_validator():
    try:
        command("remove validator", ["docker", "rm", "-f", VALIDATE_NAME], 10,
                allowed=(0, 1))
    except (RuntimeError, OSError) as exc:
        print(f"validator cleanup failed: {exc}", file=sys.stderr)


def preflight(values):
    compose("compose config", ["config", "-q"], 30)
    bind = values["WBB_MEDIA_BIND_IP"]
    for key in ("WBB_MEDIA_HTTPS_PORT", "WBB_MEDIA_ICE_PORT"):
        port = int(values[key])
        with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
            try:
                sock.bind((bind, port))
            except OSError as exc:
                raise RuntimeError(f"port unavailable: {port}") from exc
    existing = compose("compose ps", ["--profile", "cpu", "--profile", "vaapi", "ps", "-aq"], 30)
    if existing.strip():
        raise RuntimeError("dedicated project already has containers; stop it explicitly first")
    if values["WBB_MEDIA_ENCODER"] == "vaapi" and not Path("/dev/dri/renderD128").exists():
        raise RuntimeError("VAAPI render node missing; no CPU fallback")
    try:
        command("Caddy validate", ["docker", "run", "--rm", "--name", VALIDATE_NAME,
                 "--network", "none", "-v", f"{HERE / 'Caddyfile'}:/etc/caddy/Caddyfile:ro",
                 "-e", "WBB_MEDIA_AUTH_USER", "-e", "WBB_MEDIA_AUTH_HASH",
                 "-e", "WBB_MEDIA_ORIGIN", "-e", "WBB_MEDIA_HOSTNAME",
                 "-e", "WBB_MEDIA_HTTPS_PORT", CADDY_IMAGE, "caddy", "validate",
                 "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile"], 45,
                env=clean_env(values))
    finally:
        # Docker can leave the container alive if its CLI is interrupted.
        remove_validator()
    print("preflight OK (dedicated project, ports, settings, Caddyfile)", flush=True)


def stage_web(values):
    command("MediaMTX render", [sys.executable, str(HERE / "settings.py"), "render"], 10)
    staging = Path(tempfile.mkdtemp(prefix="web-build.tmp.", dir=HERE))
    source = HERE.parent / "media-single-rendition" / "web"
    try:
        shutil.copy2(source / "index.html", staging / "index.html")
        shutil.copy2(source / "app.js", staging / "app.js")
        shutil.copytree(source / "vendor", staging / "vendor")
        (staging / "config.js").write_text(
            "window.__WBB_MEDIA_CONFIG = {\n"
            "  whipPath: '/__media-check/whip/spike/source/whip',\n"
            "  hlsPath: '/__media-check/hls/spike/720p/index.m3u8',\n"
            "  whipBase: '/__media-check/whip/spike/source/whip',\n"
            "};\n")
        html = (staging / "index.html").read_text()
        needle = '<script src="app.js"></script>'
        if html.count(needle) != 1:
            raise RuntimeError("web UI script marker missing")
        (staging / "index.html").write_text(html.replace(
            needle, '<script src="config.js"></script>\n  ' + needle))
        target = HERE / "web-build"
        shutil.rmtree(target, ignore_errors=True)
        staging.rename(target)
    finally:
        if staging.exists():
            shutil.rmtree(staging)


def ready(values):
    fd, ca_path = tempfile.mkstemp(prefix="wbb-media-ca-")
    os.close(fd)
    ca = Path(ca_path)
    try:
        for _ in range(30):
            try:
                certificate = compose("Caddy CA extract", ["exec", "-T", "caddy", "cat",
                                      "/data/caddy/pki/authorities/local/root.crt"], 10)
                if certificate.startswith("-----BEGIN CERTIFICATE-----"):
                    ca.write_text(certificate)
                    origin = values["WBB_MEDIA_ORIGIN"]
                    hostname = values["WBB_MEDIA_HOSTNAME"]
                    port = values["WBB_MEDIA_HTTPS_PORT"]
                    bind = values["WBB_MEDIA_BIND_IP"]
                    code = command("HTTPS readiness", ["curl", "--silent", "--show-error",
                        "--max-time", "3", "--noproxy", "*", "--cacert", str(ca),
                        "--resolve", f"{hostname}:{port}:{bind}", "-o", "/dev/null",
                        "-w", "%{http_code}", origin + "/__media-check/"], 6)
                    if code == "401":
                        return
            except RuntimeError:
                pass
            time.sleep(1)
        raise RuntimeError("Caddy HTTPS/auth readiness failed (expected trusted TLS + HTTP 401)")
    finally:
        ca.unlink(missing_ok=True)


def down():
    compose("dedicated project down", ["--profile", "cpu", "--profile", "vaapi",
            "down", "--remove-orphans"], 90)


def main():
    signal.signal(signal.SIGINT, on_signal)
    signal.signal(signal.SIGTERM, on_signal)
    if len(sys.argv) != 2 or sys.argv[1] not in ("start", "preflight", "stop"):
        print("usage: lifecycle.py start|preflight|stop", file=sys.stderr)
        return 2
    started_up = False
    try:
        values = load()
        action = sys.argv[1]
        if action == "stop":
            down()
            shutil.rmtree(HERE / "web-build", ignore_errors=True)
            shutil.rmtree(HERE / "run", ignore_errors=True)
            print("dedicated project stopped")
            return 0
        preflight(values)
        if action == "preflight":
            return 0
        stage_web(values)
        started_up = True
        compose("compose up", ["--profile", values["WBB_MEDIA_ENCODER"], "up", "-d"], 180)
        ready(values)
        print(f"Ready: {values['WBB_MEDIA_ORIGIN']}/__media-check/ "
              f"(explicit {values['WBB_MEDIA_ENCODER']} encoder)")
        return 0
    except Interrupted as exc:
        print("interrupted; stopping this run", file=sys.stderr)
        status = 128 + exc.number
    except (ValueError, RuntimeError, OSError) as exc:
        print(f"start/preflight failed: {exc}", file=sys.stderr)
        status = 1
    if started_up:
        signal.signal(signal.SIGINT, signal.SIG_IGN)
        signal.signal(signal.SIGTERM, signal.SIG_IGN)
        try:
            down()
        except (RuntimeError, OSError) as exc:
            print(f"dedicated project cleanup failed: {exc}", file=sys.stderr)
    return status


if __name__ == "__main__":
    raise SystemExit(main())

#!/usr/bin/env python3
"""Termination evidence for the VAAPI spike (server-side).

Same contract as the CPU-spike regression, now in Python so every failure
propagates explicitly (no set -e-in-conditional-context holes):

  - Docker/API query errors are FAILURES (never "no encoder").
  - The ORIGINAL encoder is checked DIRECTLY via /proc/<pid>/stat of the
    stored PID; the stat read distinguishes "file missing" (process gone)
    from an actual read failure, and never masks errors through pipes.
  - `before` invalidates prior state, requires a real encoder, ready paths
    and PROGRESSING parts (two valid HTTP-200 LL-HLS samples), publishing
    the state only after all checks pass.
  - `after`: convergence limit (original gone, paths inactive), then a full
    >=6s observation window INCLUDING a final sample at t>=window, rejecting
    reappearance (404->200), query errors and new part/sequence progression.

Env overrides (for the error-injection selftest):
  WBB_TERM_API_BASE (default http://127.0.0.1:19990)
  WBB_TERM_HLS_BASE (default http://127.0.0.1:18888)
  WBB_TERM_CONV_LIMIT (default 10), WBB_TERM_WINDOW (default 6),
  WBB_TERM_INTERVAL (default 2)
"""
import json
import os
import re
import subprocess
import sys
import time
import urllib.error
import urllib.request

STATE = "/tmp/wbb-media-vaapi-term"
API_BASE = os.environ.get("WBB_TERM_API_BASE", "http://127.0.0.1:19990")
HLS_BASE = os.environ.get("WBB_TERM_HLS_BASE", "http://127.0.0.1:18888")
CONV_LIMIT = float(os.environ.get("WBB_TERM_CONV_LIMIT", "10"))
WINDOW = float(os.environ.get("WBB_TERM_WINDOW", "6"))
INTERVAL = float(os.environ.get("WBB_TERM_INTERVAL", "2"))


def fail(msg: str) -> "None":
    print(f"FAIL: {msg}", file=sys.stderr)
    raise SystemExit(1)


def dexec(cmd: str) -> str:
    """docker compose exec with explicit failure propagation."""
    proc = subprocess.run(
        ["docker", "compose", "exec", "-T", "transcoder", "sh", "-c", cmd],
        capture_output=True, text=True,
    )
    if proc.returncode != 0:
        fail(f"조회 오류(transcoder: {cmd}): rc={proc.returncode} {proc.stderr.strip()[:200]}")
    return proc.stdout


def compose_ps_q(service: str) -> str:
    proc = subprocess.run(
        ["docker", "compose", "ps", "-q", service],
        capture_output=True, text=True,
    )
    if proc.returncode != 0:
        fail(f"compose ps -q {service} 실패: {proc.stderr.strip()[:200]}")
    return proc.stdout.strip()


def http(url: str, timeout: float = 4.0):
    """Returns (status, body). Network errors raise -> fail()."""
    req = urllib.request.Request(url, headers={"Cache-Control": "no-store"})
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            return resp.status, resp.read().decode("utf-8", "replace")
    except urllib.error.HTTPError as e:
        return e.code, ""
    except Exception as e:  # timeout / connection
        fail(f"HTTP 조회 오류({url}): {e}")


def paths_state():
    status, body = http(f"{API_BASE}/v3/paths/list")
    if status != 200:
        fail(f"관리 API 조회 실패: HTTP {status}")
    try:
        items = {i["name"]: i for i in json.loads(body)["items"]}
    except Exception as e:
        fail(f"관리 API JSON 파싱 실패: {e}")
    return items


def paths_all(items, want_ready: bool) -> bool:
    for name in ("spike/source", "spike/720p"):
        it = items.get(name)
        if want_ready:
            if not (it and it.get("ready") is True):
                return False
        else:
            if not (it is None or (it.get("ready") is False and it.get("source") is None)):
                return False
    return True


def stat_probe(pid: str) -> str:
    """Direct /proc/<pid>/stat query. Returns 'GONE' or the starttime field.
    Distinguishes file-missing (gone) from read failures (hard error)."""
    out = dexec(
        f'if [ -e /proc/{pid}/stat ]; then awk \'{{print $22}}\' /proc/{pid}/stat || exit 3; else echo GONE; fi'
    ).strip()
    if out == "GONE":
        return "GONE"
    if not re.fullmatch(r"[0-9]+", out):
        fail(f"/proc/{pid}/stat 조회 이상: '{out}'")
    return out


def orig_check(pid: str, starttime: str) -> str:
    st = stat_probe(pid)
    if st == "GONE":
        return "gone"
    if st == starttime:
        return "alive"
    return "reused"  # PID recycled; the original process is gone


def current_encoder():
    pid = dexec("pidof ffmpeg 2>/dev/null").split()[0] if dexec("pidof ffmpeg 2>/dev/null").split() else None
    if pid is None:
        return None
    st = stat_probe(pid)
    return pid, st


def variant_url() -> str:
    status, body = http(f"{HLS_BASE}/spike/720p/index.m3u8")
    if status != 200:
        fail(f"master playlist 조회 실패: HTTP {status}")
    m = re.search(r"^([^#\s][^\s]*)$", body, re.M)
    if not m:
        fail("variant URI 없음")
    return f"{HLS_BASE}/spike/720p/{m.group(1)}"


def parse_media(body: str):
    if "#EXTM3U" not in body:
        fail("broken playlist (no EXTM3U)")
    m = re.search(r"#EXT-X-MEDIA-SEQUENCE:(\d+)", body)
    if not m:
        fail("MEDIA-SEQUENCE 없음/비숫자")
    parts = re.findall(r'^#EXT-X-PART:.*URI="([^"]+)"', body, re.M)
    if "EXT-X-PART-INF" in body and not parts:
        fail("PART-INF without parts")
    return m.group(1), (parts[-1] if parts else None)


def fetch_variant(url: str):
    status, body = http(url)
    if status == 200:
        msn, part = parse_media(body)
        return "200", msn, part
    if status in (404, 410):
        return str(status), None, None
    return str(status), None, None


def do_before():
    for suffix in ("", ".ct", ".url", ".master", ".media", ".paths.json", ".tmp"):
        try:
            os.remove(STATE + suffix)
        except FileNotFoundError:
            pass

    ct = compose_ps_q("transcoder")
    if not ct:
        fail("transcoder 컨테이너 없음")

    enc = current_encoder()
    if enc is None:
        fail("정지 전 인코더 없음 — 종료 증거를 만들 수 없음")
    pid, st = enc
    if int(st) <= 0:
        fail(f"잘못된 starttime(pid={pid}): {st}")

    items = paths_state()
    if not paths_all(items, want_ready=True):
        fail(f"정지 전 source/output 이 ready 가 아님: {items}")

    url = variant_url()
    c1, m1, p1 = fetch_variant(url)
    if c1 != "200":
        fail(f"before 표본1이 200 아님({c1})")
    time.sleep(INTERVAL)
    c2, m2, p2 = fetch_variant(url)
    if c2 != "200":
        fail(f"before 표본2가 200 아님({c2}) — 진행 아님")
    if m1 == m2 and p1 == p2:
        fail("정지 전 part 진행 없음 — 살아있는 출력이 아님")

    # Publish complete state only after every check passed. The main state
    # file is replaced LAST as the completion marker.
    with open(STATE + ".ct.tmp", "w") as f:
        f.write(ct + "\n")
    with open(STATE + ".url.tmp", "w") as f:
        f.write(url + "\n")
    with open(STATE + ".tmp", "w") as f:
        f.write(f"{pid} {st}\n")
    os.replace(STATE + ".ct.tmp", STATE + ".ct")
    os.replace(STATE + ".url.tmp", STATE + ".url")
    os.replace(STATE + ".tmp", STATE)
    print(f"BEFORE OK: pid={pid} starttime={st} ct={ct} msn {m1}→{m2}")


def do_after():
    try:
        with open(STATE) as f:
            pid, st = f.read().split()
        ct = open(STATE + ".ct").read().strip()
        url = open(STATE + ".url").read().strip()
    except Exception as e:
        fail(f"유효한 before 상태 없음: {e}")

    ctnow = compose_ps_q("transcoder")
    if not ctnow:
        fail("transcoder 컨테이너 조회 불가")
    if ctnow != ct:
        fail(f"컨테이너 ID 불일치({ct} → {ctnow})")

    # Convergence (limit): original gone + paths inactive.
    t0 = time.monotonic()
    while True:
        res = orig_check(pid, st)
        if res != "alive":
            break
        if time.monotonic() - t0 >= CONV_LIMIT:
            fail(f"원래 인코더가 {CONV_LIMIT:.0f}초 내 종료하지 않음 (still alive)")
        time.sleep(min(1.0, INTERVAL / 2))
    t0 = time.monotonic()
    while True:
        if paths_all(paths_state(), want_ready=False):
            break
        if time.monotonic() - t0 >= CONV_LIMIT:
            fail(f"경로가 {CONV_LIMIT:.0f}초 내 비활성화되지 않음")
        time.sleep(min(1.0, INTERVAL / 2))
    print("convergence OK (original encoder gone, paths inactive)")

    # Full window observation WITH a final sample at t>=WINDOW.
    t0 = time.monotonic()
    samples = 0
    gone = False
    last_msn = last_part = None
    while True:
        if compose_ps_q("transcoder") != ct:
            fail("관측 중 컨테이너 불일치")
        res = orig_check(pid, st)
        if res == "alive":
            fail("원래 인코더 재등장")
        if res not in ("gone", "reused"):
            fail(f"orig_check 알 수 없는 결과: {res}")
        if not paths_all(paths_state(), want_ready=False):
            fail("관측 중 경로 재활성")
        code, msn, part = fetch_variant(url)
        if code in ("404", "410"):
            gone = True
        elif code == "200":
            if gone:
                fail("재등장: 404 후 200 복귀")
            if last_msn is not None and (msn != last_msn or part != last_part):
                fail(f"새 part/sequence 진행: msn {last_msn}→{msn} / part {last_part}→{part}")
            last_msn, last_part = msn, part
        else:
            fail(f"variant HTTP {code} — 종료 증거 아님")
        samples += 1
        if time.monotonic() - t0 >= WINDOW:
            break
        time.sleep(INTERVAL)
    if samples < 3:
        fail(f"관측 표본 부족({samples})")
    print(f"TERMINATION_OK (encoder/paths/parts verified over >={WINDOW:.0f}s window incl. final sample, samples={samples})")


def main():
    if len(sys.argv) != 2 or sys.argv[1] not in ("before", "after"):
        print("usage: verify-termination.py before|after", file=sys.stderr)
        raise SystemExit(2)
    do_before() if sys.argv[1] == "before" else do_after()


if __name__ == "__main__":
    main()

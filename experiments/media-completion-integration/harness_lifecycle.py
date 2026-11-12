"""Bounded ownership of verifier children; never touches unrelated PIDs."""
import os
import signal
import subprocess
import time


def group_alive(pgid):
    try:
        os.killpg(pgid, 0)
        return True
    except ProcessLookupError:
        return False
    except PermissionError:
        # macOS can report EPERM while the just-killed leader is unreaped.
        # Treat uncertainty as a live group and continue escalation.
        return True


def terminate_group(process, grace=2.0):
    """Reap the direct child and all descendants of our new process group."""
    pgid = process.pid
    if group_alive(pgid):
        try:
            os.killpg(pgid, signal.SIGTERM)
        except ProcessLookupError:
            pass
    try:
        process.wait(timeout=grace)
    except subprocess.TimeoutExpired:
        pass
    deadline = time.monotonic() + grace
    while group_alive(pgid) and time.monotonic() < deadline:
        time.sleep(.05)
    if group_alive(pgid):
        try:
            os.killpg(pgid, signal.SIGKILL)
        except ProcessLookupError:
            pass
    try:
        process.wait(timeout=3)
    except subprocess.TimeoutExpired as error:
        raise RuntimeError(f'owned child PID {pgid} was not reaped') from error
    deadline = time.monotonic() + 3
    while group_alive(pgid) and time.monotonic() < deadline:
        time.sleep(.05)
    if group_alive(pgid):
        raise RuntimeError(f'owned process group {pgid} remains')


def run_owned(args, *, cwd, timeout, stdout=None, stderr=None, env=None):
    process = subprocess.Popen(args, cwd=cwd, stdout=stdout, stderr=stderr,
                               env=env, start_new_session=True)
    try:
        code = process.wait(timeout=timeout)
    except subprocess.TimeoutExpired as error:
        terminate_group(process)
        raise TimeoutError(f'owned command exceeded {timeout}s: {args[0]}') from error
    if group_alive(process.pid):
        terminate_group(process)
        raise RuntimeError(f'owned command left descendants: {args[0]}')
    return code

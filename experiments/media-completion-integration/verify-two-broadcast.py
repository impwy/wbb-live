"""Run two public HTTPS Chrome flows concurrently on the isolated 07-A2 stack.

B keeps decoding HLS while A stops. This script records B's durable media
identity on both sides of A's stop and requires repeated audio FFT samples.
"""
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import time

from harness_lifecycle import group_alive, run_owned, terminate_group
from hold_continuity import verify_hold_continuity

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[1]
B_LOG = Path('/tmp/wbb-07a2-two-b.log')
A_LOG = Path('/tmp/wbb-07a2-two-a.log')
PG = 'wbb-media-completion-integration-postgres-1'


def query(sql):
    return subprocess.check_output(
        ['docker', 'exec', PG, 'psql', '-U', 'wbb', '-d', 'wbb', '-At', '-c', sql],
        text=True, timeout=5,
    ).strip()


def identity(sid):
    if not re.fullmatch(r'[0-9a-f-]{36}', sid):
        raise RuntimeError('invalid session id')
    return query(
        "SELECT s.state||','||r.run_id::text||','||r.pid::text||','||"
        "r.process_start_ticks::text||','||r.fencing_token::text||','||"
        "o.completion_epoch||','||o.completion_epoch_sequence::text "
        "FROM broadcast_sessions s JOIN media_worker_runs r ON r.broadcast_session_id=s.id "
        "JOIN media_create_operations o ON o.broadcast_session_id=s.id "
        f"WHERE s.id='{sid}'"
    )


def result(log):
    lines = log.read_text().splitlines()
    return json.loads(next(line for line in reversed(lines) if line.startswith('{')))


def main():
    stage = sys.argv[1] if len(sys.argv) > 1 else None
    allowed = {'before_register', 'before_session_initialize', 'during_auth',
               'before_add_publisher', 'during_rtp_callback'}
    if stage is not None and stage not in allowed:
        raise RuntimeError('invalid product barrier stage')
    if query("SELECT count(*) FROM broadcast_sessions WHERE state IN ('starting','live','stopping')") != '0':
        raise RuntimeError('isolated stack has an active session')
    command = ['node', str(HERE / 'verify-browser.mjs')]
    hold = '30' if stage else '50'
    env = dict(os.environ, WBB_ISOLATED_HOLD_LIVE_SECONDS=hold,
               WBB_CHECK_CONTINUOUS_LEASE='1', WBB_REQUIRE_CONTINUOUS_PLAYBACK='1')
    with B_LOG.open('w') as b_output:
        b = subprocess.Popen(command, cwd=ROOT, stdout=b_output, stderr=subprocess.STDOUT,
                             env=env, start_new_session=True)
        failure = None
        cleanup = []
        try:
            sid = None
            deadline = time.monotonic() + 90
            while time.monotonic() < deadline and b.poll() is None:
                match = re.search(r'holding live ' + hold + r's session=([0-9a-f-]{36})', B_LOG.read_text())
                if match:
                    sid = match.group(1)
                    break
                time.sleep(.25)
            if sid is None:
                raise RuntimeError('B did not reach LIVE hold')
            before = identity(sid)
            a_command = (['node', str(HERE / 'verify-product-barrier.mjs'), stage]
                         if stage else command)
            with A_LOG.open('w') as a_output:
                a_code = run_owned(a_command, cwd=ROOT, stdout=a_output,
                                   stderr=subprocess.STDOUT, timeout=110)
            a_result = result(A_LOG) if a_code == 0 else {}
            if a_code != 0 or (a_result.get('completed') if stage else a_result.get('completionProved')) is not True:
                raise RuntimeError('A did not stop with completed proof')
            after = identity(sid)
            if not before.startswith('live,') or after != before:
                raise RuntimeError('B changed while A stopped')
            if b.wait(timeout=90) != 0:
                raise RuntimeError('B hold or cleanup failed')
            observed = result(B_LOG)
            if observed.get('completionProved') is not True or observed.get('holdAudioSamples', 0) < (int(hold)+4)//5 or observed.get('holdProgressFrames', 0) < 20:
                raise RuntimeError('B video, audio, or completion proof missing')
            aligned = verify_hold_continuity(observed.get('holdSamples', []),
                                             a_result.get('holdStartWallMs'),
                                             a_result.get('holdEndWallMs')) if stage else None
            if query("SELECT count(*) FROM broadcast_sessions WHERE state IN ('starting','live','stopping')") != '0':
                raise RuntimeError('owned session remained after both browser flows')
            print(json.dumps({'stage':stage,'A': 'ended', 'B': 'ended', 'BIdentityPreserved': True,
                              'BHoldAudioSamples': observed['holdAudioSamples'],
                              'BHoldFrames': observed['holdProgressFrames'],
                              'alignedHold': aligned,
                              'logs': [str(A_LOG), str(B_LOG)]}))
        except Exception as error:
            failure = error
        finally:
            if b.poll() is None:
                try:
                    b.wait(timeout=90)
                except subprocess.TimeoutExpired:
                    cleanup.append('B verifier exceeded 90s')
            if group_alive(b.pid):
                try:
                    terminate_group(b)
                except Exception as error:
                    cleanup.append(f'B process group cleanup: {error}')
            try:
                if query("SELECT count(*) FROM broadcast_sessions WHERE state IN ('starting','live','stopping')") != '0':
                    cleanup.append('an owned session remains after B cleanup')
            except Exception as error:
                cleanup.append(f'owned session check: {error}')
        if failure or cleanup:
            raise RuntimeError('; '.join(([str(failure)] if failure else []) + cleanup))


if __name__ == '__main__':
    main()

"""Run two public HTTPS Chrome flows concurrently on the isolated 07-A2 stack.

B keeps decoding HLS while A stops. This script records B's durable media
identity on both sides of A's stop and requires repeated audio FFT samples.
"""
import json
import os
from pathlib import Path
import re
import subprocess
import time

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
    if query("SELECT count(*) FROM broadcast_sessions WHERE state IN ('starting','live','stopping')") != '0':
        raise RuntimeError('isolated stack has an active session')
    command = ['node', str(HERE / 'verify-browser.mjs')]
    env = dict(os.environ, WBB_ISOLATED_HOLD_LIVE_SECONDS='50',
               WBB_CHECK_CONTINUOUS_LEASE='1', WBB_REQUIRE_CONTINUOUS_PLAYBACK='1')
    with B_LOG.open('w') as b_output:
        b = subprocess.Popen(command, cwd=ROOT, stdout=b_output, stderr=subprocess.STDOUT, env=env)
        try:
            sid = None
            deadline = time.monotonic() + 90
            while time.monotonic() < deadline and b.poll() is None:
                match = re.search(r'holding live 50s session=([0-9a-f-]{36})', B_LOG.read_text())
                if match:
                    sid = match.group(1)
                    break
                time.sleep(.25)
            if sid is None:
                raise RuntimeError('B did not reach LIVE hold')
            before = identity(sid)
            with A_LOG.open('w') as a_output:
                a = subprocess.run(command, cwd=ROOT, stdout=a_output,
                                   stderr=subprocess.STDOUT, timeout=110, check=False)
            if a.returncode != 0 or result(A_LOG).get('completionProved') is not True:
                raise RuntimeError('A did not stop with completed proof')
            after = identity(sid)
            if not before.startswith('live,') or after != before:
                raise RuntimeError('B changed while A stopped')
            if b.wait(timeout=90) != 0:
                raise RuntimeError('B hold or cleanup failed')
            observed = result(B_LOG)
            if observed.get('completionProved') is not True or observed.get('holdAudioSamples', 0) < 10 or observed.get('holdProgressFrames', 0) < 20:
                raise RuntimeError('B video, audio, or completion proof missing')
            print(json.dumps({'A': 'ended', 'B': 'ended', 'BIdentityPreserved': True,
                              'BHoldAudioSamples': observed['holdAudioSamples'],
                              'BHoldFrames': observed['holdProgressFrames'],
                              'logs': [str(A_LOG), str(B_LOG)]}))
        finally:
            if b.poll() is None:
                try:
                    b.wait(timeout=90)
                except subprocess.TimeoutExpired:
                    b.terminate()
                    raise RuntimeError('B verifier did not finish; inspect its owned session before reuse')


if __name__ == '__main__':
    main()

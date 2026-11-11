"""A→B→C on the product stack, with API/worker absent throughout B.

Uses the existing dedicated state volume. The Chrome publisher owns cleanup;
the wrapper always restarts only its API/worker if an assertion fails.
"""
import hashlib
import hmac
import json
import os
from pathlib import Path
import re
import subprocess
import time
import urllib.request

from harness_lifecycle import group_alive, run_owned, terminate_group

HERE = Path(__file__).resolve().parent
COMPOSE = ['docker', 'compose', '--env-file', '.env', '-f', 'compose.yml', '-f', 'compose.barriers.yml']
LOG = Path('/tmp/wbb-07a2-product-epoch-browser.log')
ENV = dict(line.split('=', 1) for line in (HERE / '.env').read_text().splitlines() if '=' in line)
KEY = bytes.fromhex(ENV['WBB_COMPLETION_KEY_HEX'])
ROOT, SLOT = ENV['WBB_COMPLETION_ROOT'], ENV['WBB_COMPLETION_SLOT']
MANAGEMENT = 'http://127.0.0.1:29998/v3/wbb'


def signature(*parts):
    value = b''.join(str(len(part.encode())).encode() + b':' + part.encode() for part in parts)
    return hmac.new(KEY, value, hashlib.sha256).hexdigest()


def management(path, purpose, epoch='', operation=''):
    headers = {'X-WBB-Signature': signature(purpose, ROOT, SLOT, epoch, operation)}
    if epoch:
        headers['X-WBB-Epoch'] = epoch
    request = urllib.request.Request(MANAGEMENT + path, headers=headers)
    with urllib.request.urlopen(request, timeout=4) as response:
        if response.status != 200:
            raise RuntimeError('management response was not OK')
        return json.load(response)


def lifecycle():
    end = time.monotonic() + 20
    while time.monotonic() < end:
        try:
            value = management('/lifecycle', 'lifecycle')
            if value['root'] == ROOT and value['slot'] == SLOT and value['contract_version'] == 'wbb-media-completion/1':
                return value
        except (OSError, KeyError, ValueError):
            time.sleep(.25)
    raise RuntimeError('MediaMTX lifecycle did not recover')


def compose(*args):
    if run_owned([*COMPOSE, *args], cwd=HERE, timeout=40,
                 stdout=subprocess.DEVNULL) != 0:
        raise RuntimeError(f'dedicated Compose {args[0]} failed')


def restore_services():
    errors = []
    for service in ('api', 'worker'):
        try:
            compose('up', '-d', '--no-build', '--no-deps', service)
        except Exception as error:
            errors.append(f'{service}: {error}')
    if errors:
        raise RuntimeError('; '.join(errors))
    end = time.monotonic() + 40
    while time.monotonic() < end:
        try:
            subprocess.run(['docker', 'exec', 'wbb-media-completion-integration-api-1',
                            'wget', '-q', '-O', '/dev/null',
                            'http://127.0.0.1:8080/api/v1/health/ready'],
                           check=True, stdout=subprocess.DEVNULL,
                           stderr=subprocess.DEVNULL, timeout=4)
            return
        except (subprocess.CalledProcessError, subprocess.TimeoutExpired):
            time.sleep(.5)
    raise RuntimeError('API readiness did not recover')


def sql(statement):
    return subprocess.check_output(['docker', 'exec', 'wbb-media-completion-integration-postgres-1',
                                    'psql', '-v', 'ON_ERROR_STOP=1', '-U', 'wbb', '-d', 'wbb',
                                    '-At', '-c', statement], text=True, timeout=5).strip()


def result():
    return json.loads(next(line for line in reversed(LOG.read_text().splitlines()) if line.startswith('{')))


def main():
    if sql("SELECT count(*) FROM broadcast_sessions WHERE state IN ('starting','live','stopping')") != '0':
        raise RuntimeError('dedicated stack has an active session')
    first = lifecycle()
    env = dict(os.environ, WBB_ISOLATED_HOLD_LIVE_SECONDS='45', WBB_EXPECT_MTX_RESTART='1')
    restore_needed = False
    failure = None
    cleanup = []
    summary = None
    with LOG.open('w') as output:
        browser = subprocess.Popen(['node', 'verify-browser.mjs'], cwd=HERE,
                                   stdout=output, stderr=subprocess.STDOUT, env=env,
                                   start_new_session=True)
        try:
            sid = None
            deadline = time.monotonic() + 60
            while time.monotonic() < deadline and browser.poll() is None:
                match = re.search(r'holding live 45s session=([0-9a-f-]{36})', LOG.read_text())
                if match:
                    sid = match.group(1)
                    break
                time.sleep(.25)
            if sid is None:
                raise RuntimeError('publisher did not reach LIVE hold')
            row = sql(f"SELECT id::text||','||completion_epoch||','||completion_epoch_sequence::text "
                      f"FROM media_create_operations WHERE broadcast_session_id='{sid}'")
            operation, pinned_epoch, pinned_sequence = row.split(',')
            if pinned_epoch != first['epoch'] or int(pinned_sequence) != first['epoch_sequence']:
                raise RuntimeError('product did not pin original epoch')
            restore_needed = True  # A partial Compose stop still needs recovery.
            if os.environ.get('WBB_EPOCH_INJECT_PARTIAL_STOP') == '1':
                compose('stop', 'worker')
                raise RuntimeError('injected partial Compose stop')
            compose('stop', 'worker', 'api')
            compose('restart', 'mediamtx')
            second = lifecycle()
            compose('restart', 'mediamtx')
            third = lifecycle()
            if len({first['epoch'], second['epoch'], third['epoch']}) != 3 or [first['epoch_sequence']+1,first['epoch_sequence']+2] != [second['epoch_sequence'],third['epoch_sequence']]:
                raise RuntimeError('A→B→C epoch history did not advance')
            history = management(f'/operations/{operation}?epoch={pinned_epoch}', 'query', pinned_epoch, operation)
            if history['state'] != 'completed' or history['epoch'] != pinned_epoch or history['epoch_sequence'] != first['epoch_sequence']:
                raise RuntimeError('retired A operation lacks exact completed evidence')
            unknown_epoch = 'f' * 32 if third['epoch'] != 'f' * 32 else 'e' * 32
            unknown = management(f'/operations/{operation}?epoch={unknown_epoch}', 'query', unknown_epoch, operation)
            if unknown['state'] != 'unknown_epoch':
                raise RuntimeError('unknown epoch was accepted as completion')
            restore_services()
            restore_needed = False
            if browser.wait(timeout=130) != 0:
                raise RuntimeError('product browser did not recover and clean up')
            verified = result()
            if verified['finalState'] != 'ended' or verified['completionProved'] is not True or not verified['ffmpegExited'] or not verified['ingressAbsent']:
                raise RuntimeError('product did not persist retired A proof and clean media')
            persisted = sql(f"SELECT completion_epoch||','||completion_epoch_sequence::text||','||completion_evidence_kind FROM media_create_operations WHERE id='{operation}'")
            if persisted != f'{pinned_epoch},{first["epoch_sequence"]},completed':
                raise RuntimeError('product changed pinned tuple or missed completed evidence')
            summary={'A':first['epoch_sequence'],'B':second['epoch_sequence'],'C':third['epoch_sequence'],
                     'BUnobservedByProduct':True,'historicalA':'completed','unknown':'unknown_epoch',
                     'productEnded':True,'ffmpegAbsent':True,'mediaAbsent':True,'sessionId':sid}
        except Exception as error:
            failure = error
        finally:
            if restore_needed:
                try:
                    restore_services()
                except Exception as error:
                    cleanup.append(f'API/worker restoration: {error}')
            if browser.poll() is None:
                try:
                    browser.wait(timeout=130)
                except subprocess.TimeoutExpired:
                    cleanup.append('browser verifier exceeded 130s')
            if group_alive(browser.pid):
                try:
                    terminate_group(browser)
                except Exception as error:
                    cleanup.append(f'browser process group: {error}')
            try:
                if sql("SELECT count(*) FROM broadcast_sessions WHERE state IN ('starting','live','stopping')") != '0':
                    cleanup.append('an owned session remains after epoch test')
            except Exception as error:
                cleanup.append(f'owned session check: {error}')
    if failure or cleanup:
        raise RuntimeError('; '.join(([str(failure)] if failure else []) + cleanup))
    print(json.dumps(summary))


if __name__ == '__main__':
    main()

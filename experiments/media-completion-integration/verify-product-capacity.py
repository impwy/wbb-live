"""Disposable product capacity=2 matrix; restores the original stack/volume.

Uses a separate DB, slot anchor, history volume and secret. The existing
integration project is paused only after its active/unresolved count is zero.
"""
import json
import os
from pathlib import Path
import re
import secrets
import subprocess
import tempfile
import time
from urllib.parse import urlsplit, urlunsplit

from harness_lifecycle import group_alive, run_owned, terminate_group

HERE = Path(__file__).resolve().parent
BASE = ['docker', 'compose', '--env-file', '.env', '-f', 'compose.yml', '-f', 'compose.barriers.yml']
DB_CONTAINER = 'wbb-media-completion-integration-postgres-1'
API_CONTAINER = 'wbb-media-completion-integration-api-1'
LOG_A = Path('/tmp/wbb-07a2-capacity-a.log')
LOG_B = Path('/tmp/wbb-07a2-capacity-b.log')
LOG_C = Path('/tmp/wbb-07a2-capacity-third.log')


def run(args, timeout=40):
    if run_owned(args, cwd=HERE, timeout=timeout, stdout=subprocess.DEVNULL) != 0:
        raise RuntimeError(f'isolated command failed: {args[0]}')


def output(args, timeout=8):
    with tempfile.TemporaryFile(mode='w+t') as stream:
        if run_owned(args, cwd=HERE, timeout=timeout, stdout=stream) != 0:
            raise RuntimeError(f'isolated read failed: {args[0]}')
        stream.seek(0)
        return stream.read().strip()


def sql(database, statement):
    return output(['docker', 'exec', DB_CONTAINER, 'psql', '-v', 'ON_ERROR_STOP=1',
                   '-U', 'wbb', '-d', database, '-At', '-c', statement])


def compose(prefix, *args):
    run([*prefix, *args])


def ready():
    end = time.monotonic() + 50
    while time.monotonic() < end:
        try:
            if run_owned(['docker', 'exec', API_CONTAINER, 'wget', '-q', '-O', '/dev/null',
                          'http://127.0.0.1:8080/api/v1/health/ready'], cwd=HERE,
                         timeout=4, stdout=subprocess.DEVNULL,
                         stderr=subprocess.DEVNULL) == 0:
                return
        except TimeoutError:
            pass
        time.sleep(.5)
    raise RuntimeError('product API readiness did not recover')


def switch(prefix):
    # no-deps prevents Compose from replacing the preserved PG, Redis or Caddy
    errors = []
    for service in ('barrier-proxy', 'mediamtx', 'api', 'worker'):
        try:
            compose(prefix, 'up', '-d', '--no-build', '--no-deps', '--force-recreate', service)
        except Exception as error:
            errors.append(f'{service}: {error}')
    if errors:
        raise RuntimeError('; '.join(errors))
    ready()


def wait_hold(process, log):
    end = time.monotonic() + 60
    while time.monotonic() < end:
        if process.poll() is not None:
            raise RuntimeError(f'publisher exited before LIVE: {log}')
        if re.search(r'holding live 50s session=([0-9a-f-]{36})', log.read_text()):
            return
        time.sleep(.2)
    raise RuntimeError(f'publisher did not reach LIVE: {log}')


def main():
    if sql('wbb', "SELECT count(*) FROM broadcast_sessions WHERE state IN ('starting','live','stopping')") != '0':
        raise RuntimeError('original dedicated stack has an active session')
    if sql('wbb', "SELECT count(*) FROM media_create_operations WHERE state<>'closed'") != '0':
        raise RuntimeError('original dedicated stack has an unresolved operation')
    base_env = dict(line.split('=', 1) for line in (HERE / '.env').read_text().splitlines() if '=' in line)
    suffix = secrets.token_hex(4)
    database = f'wbb_07a2_cap_{suffix}'
    volume = f'wbb-07a2-capacity-{suffix}'
    private = None
    created_db = created_volume = switched = False
    children = []
    cleanup = []
    result = None
    with tempfile.TemporaryDirectory(prefix='wbb-07a2-capacity-') as temp:
        folder = Path(temp)
        env_file, anchor = folder / 'private.env', folder / 'anchor.json'
        anchor.write_text(json.dumps({'root': f'cap-{secrets.token_hex(12)}', 'slot': f'cap-{secrets.token_hex(12)}'}) + '\n')
        parsed = urlsplit(base_env['WBB_DATABASE_URL'])
        values = dict(base_env)
        values.update(WBB_DATABASE_URL=urlunsplit(parsed._replace(path='/' + database)),
                      WBB_COMPLETION_ROOT=json.loads(anchor.read_text())['root'],
                      WBB_COMPLETION_SLOT=json.loads(anchor.read_text())['slot'],
                      WBB_COMPLETION_KEY_HEX=secrets.token_hex(32),
                      WBB_COMPLETION_ANCHOR_FILE=str(anchor),
                      WBB_CAPACITY_VOLUME=volume, WBB_CAPACITY_DB=database)
        env_file.write_text(''.join(f'{name}={value}\n' for name, value in values.items()))
        env_file.chmod(0o600)
        anchor.chmod(0o600)
        private = ['docker', 'compose', '--env-file', str(env_file), '-f', 'compose.yml',
                   '-f', 'compose.barriers.yml', '-f', 'compose.capacity.yml']
        try:
            run(['docker', 'volume', 'create', volume])
            created_volume = True
            sql('postgres', f'CREATE DATABASE {database}')
            created_db = True
            config = json.loads(output([*private, 'config', '--format', 'json']))
            mounts = [m for m in config['services']['mediamtx']['volumes'] if m['target'] == '/integration/state']
            if len(mounts) != 1 or mounts[0]['source'] != 'capacity-state' or config['volumes']['capacity-state']['name'] != volume:
                raise RuntimeError('capacity state mount is not isolated')
            compose(private, 'run', '--rm', '--no-deps', '--entrypoint', '/wbb-bootstrap', 'mediamtx', '/dev/null')
            switched = True  # Even a partially failed stop requires restoration.
            if os.environ.get('WBB_CAPACITY_INJECT_PARTIAL_STOP') == '1':
                compose(BASE, 'stop', 'worker')
                raise RuntimeError('injected partial Compose stop')
            compose(BASE, 'stop', 'worker', 'api', 'mediamtx', 'barrier-proxy')
            switch(private)
            for log in (LOG_A, LOG_B):
                env = dict(os.environ, WBB_TEST_ENV_FILE=str(env_file), WBB_CAPACITY_DB=database,
                           WBB_ISOLATED_HOLD_LIVE_SECONDS='50', WBB_CHECK_CONTINUOUS_LEASE='1',
                           WBB_REQUIRE_CONTINUOUS_PLAYBACK='1')
                with log.open('w') as stream:
                    children.append(subprocess.Popen(['node', 'verify-browser.mjs'], cwd=HERE,
                                                     stdout=stream, stderr=subprocess.STDOUT,
                                                     env=env, start_new_session=True))
            for process, log in zip(children, (LOG_A, LOG_B)):
                wait_hold(process, log)
            if sql(database, "SELECT count(*) FROM broadcast_sessions WHERE state='live'") != '2':
                raise RuntimeError('both real product publishers are not LIVE')
            with LOG_C.open('w') as stream:
                third_code = run_owned(['node', 'verify-product-capacity.mjs'], cwd=HERE,
                                       stdout=stream, stderr=subprocess.STDOUT,
                                       env=dict(os.environ, WBB_TEST_ENV_FILE=str(env_file)), timeout=35)
            if third_code != 0:
                raise RuntimeError('third public product publisher failed capacity assertion')
            for process, log in zip(children, (LOG_A, LOG_B)):
                if process.wait(timeout=110) != 0:
                    raise RuntimeError(f'product publisher failed: {log}')
            if sql(database, "SELECT count(*) FROM broadcast_sessions WHERE state='ended'") != '2':
                raise RuntimeError('real publishers did not both end')
            if sql(database, "SELECT count(*) FROM broadcast_sessions WHERE state='stopping'") != '1':
                raise RuntimeError('unknown third operation was not retained in STOPPING')
            result = {'capacity': 2, 'publishers': 'two LIVE then ENDED with continued video/audio',
                      'third': 'public 502, close capacity_unknown/503, retained STOPPING',
                      'logs': [str(LOG_A), str(LOG_B), str(LOG_C)]}
        finally:
            for process in children:
                try:
                    if group_alive(process.pid):
                        terminate_group(process)
                    else:
                        process.wait(timeout=3)
                except Exception as error:
                    cleanup.append(f'publisher cleanup: {error}')
            if switched:
                try:
                    compose(private, 'stop', 'worker', 'api', 'mediamtx', 'barrier-proxy')
                except Exception as error:
                    cleanup.append(f'capacity stack stop: {error}')
                try:
                    switch(BASE)
                    if sql('wbb', "SELECT count(*) FROM broadcast_sessions WHERE state IN ('starting','live','stopping')") != '0':
                        raise RuntimeError('original dedicated stack active after restore')
                except Exception as error:
                    cleanup.append(f'original stack restore: {error}')
            if created_db and not any('original stack restore' in item for item in cleanup):
                try:
                    sql('postgres', f'DROP DATABASE {database} WITH (FORCE)')
                except Exception as error:
                    cleanup.append(f'disposable DB cleanup: {error}')
            if created_volume and not any('original stack restore' in item for item in cleanup):
                try:
                    run(['docker', 'volume', 'rm', volume])
                except Exception as error:
                    cleanup.append(f'disposable volume cleanup: {error}')
    if cleanup:
        raise RuntimeError('; '.join(cleanup))
    print(json.dumps(result))


if __name__ == '__main__':
    main()

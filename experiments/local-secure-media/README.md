# 인증 미디어 서버 로컬 검증

04-B1의 인증 WHIP, MediaMTX·FFmpeg worker, 익명 LL-HLS 접근 제어를 확인하는 전용 환경이다. Flutter 송출 화면은 후속 04-B2이며 이 환경의 루트 페이지는 검증용 텍스트다. 실제 화면·카메라 권한을 요청하지 않고 두 Chrome 프로세스에서 합성 영상과 두 음원을 전송한다.

필요 환경: Docker Compose, Node.js/npm, 설치된 Google Chrome. 사용 포트는 loopback HTTPS 19443, PostgreSQL 55438, ICE UDP 19391이다. 기존 미디어 실험의 포트와 구분된다. 외부 서비스 배포 구성은 아니다.

저장소 루트에서 서로 다른 임의 값을 생성한다. 기존 `.env`가 있으면 덮어쓰지 않는다.

```sh
python3 - <<'PY'
from pathlib import Path
import secrets
p = Path('experiments/local-secure-media/.env')
password = secrets.token_hex(24)
with p.open('x') as f:
    f.write(f'WBB_DB_PASSWORD={password}\n')
    f.write(f'WBB_DATABASE_URL=postgres://wbb:{password}@postgres:5432/wbb\n')
    f.write(f'WBB_JWT_SECRET={secrets.token_hex(32)}\n')
    f.write(f'WBB_MEDIA_SECRET={secrets.token_hex(32)}\n')
p.chmod(0o600)
PY

docker compose --env-file experiments/local-secure-media/.env -f experiments/local-secure-media/compose.yml up -d --build
npm ci --prefix experiments/media-single-rendition
node experiments/local-secure-media/verify-browser.mjs
```

검증기는 로컬 Caddy 인증서의 공개키를 Chrome에 한정하여 허용한다. 시스템 인증서 저장소를 변경하지 않는다. 정상 종료 시 영상 디코딩·두 음원 피크·ENDED·FFmpeg 종료·해당 회차 ingress/RTSP 부재와 이전 HLS 접근 거절을 출력한다. 실패와 타임아웃은 성공으로 취급하지 않는다.

지속 재생과 lease 관측:

```sh
WBB_ISOLATED_HOLD_LIVE_SECONDS=75 WBB_CHECK_CONTINUOUS_LEASE=1 WBB_REQUIRE_CONTINUOUS_PLAYBACK=1 node experiments/local-secure-media/verify-browser.mjs
```

재시작·장애 옵션은 검증 도중 해당 전용 서비스에 실제 장애를 주는 별도 조정이 필요하다. 옵션만 설정하고 장애를 생략한 결과를 장애 검증으로 주장하지 않는다. `control-delay-proxy.mjs`는 내부 제어 지연 주입용이며 기본 Compose 경로에는 포함되지 않는다.

실행 종료(데이터 볼륨 유지):

```sh
docker compose --env-file experiments/local-secure-media/.env -f experiments/local-secure-media/compose.yml down
```

응답 유실로 resource 주소를 얻지 못한 작업은 STOPPING에 남을 수 있다. 자동 회수는 04-B/07의 미완료 범위다. [검증 결과와 제한](../../docs/verification/04b-local-secure-media.md)을 참고한다.

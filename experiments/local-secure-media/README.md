# 인증 미디어 서버 로컬 검증

04-B1의 인증 WHIP, MediaMTX·FFmpeg worker, 익명 LL-HLS 접근 제어와04-B2 Flutter 화면을 확인하는 전용 환경이다. 기본 구성의 루트는 검증용 텍스트이며, 아래 Flutter overlay를 적용하면 제품 화면을 제공한다. 자동 검증기는 실제 화면·카메라 권한을 요청하지 않고 두 Chrome 프로세스에서 합성 영상과 두 음원을 전송한다.

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

## Flutter 제품 화면

추가 환경은 Flutter SDK이며 `flutter`가 PATH에 있어야 한다. 위 `.env` 준비 후 저장소 루트에서 실행한다. 기존 전용 스택에 활성 방송이 있다면 먼저 종료한다. overlay 적용은 MediaMTX를 재생성할 수 있다.

```sh
(cd wbb-frontend && flutter pub get && flutter build web --release --no-web-resources-cdn)
docker compose --env-file experiments/local-secure-media/.env \
  -f experiments/local-secure-media/compose.yml \
  -f experiments/local-secure-media/compose.flutter.yml up -d --build
node experiments/local-secure-media/verify-flutter.mjs
```

로컬 주소는 `https://localhost:19443`이다. Caddy 내부 인증서를 사용하는 로컬 테스트 환경이며 공개 서비스 URL이 아니다. 자동 검증기는 위와 같이 해당 인증서 공개키만 허용한다.

제품 확인 흐름은 가입·로그인→내 방송→방송 등록→상세의 **화면 공유 시작**→화면/탭과 공유 소리 선택·마이크 허용이다. 별도 비로그인 브라우저에서 방송을 열고 준비가 끝나면 **재생** 또는 **소리 켜기**를 누른다. **방송 중지** 후 서버의 종료 완료와 재생 비활성화를 확인한다. 공유 소리 지원은 OS·브라우저·선택한 화면 종류에 따라 다르며, 실제 PC 공유·물리 두 기기·모바일 인수 결과는 별도로 기록한다.

Flutter overlay는 H264 초기 전송 지연을 고려해 `webrtcTrackGatherTimeout: 6s`를 사용한다. 두 트랙이 먼저 도착하면 즉시 진행한다. 합성 검증 성공을 실제 캡처·외부망 지원 완료로 간주하지 않는다. [04-B2 검증 현황](../../docs/verification/04b2-flutter-live.md)을 참고한다.

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


## 카메라·마이크 제품 확인

Flutter 상세에서 **카메라·마이크 시작**을 선택하고 두 권한을 허용한다. PC 카메라도 같은 경로를 사용한다. 익명 시청과 중지는 화면 공유와 동일하다. 카메라 송출은 숨김/pagehide에서 종료하며 복귀 시 상태를 다시 조회하고 자동 재송출하지 않는다. PC 화면 공유는 탭 숨김만으로 종료하지 않는다.

합성 카메라 검사: `node experiments/local-secure-media/verify-flutter.mjs --camera`. 합성 숨김 종료 검사: 같은 명령에 `--camera-hide`를 추가한다. 두 명령은 저장소 루트에서 전용 Flutter 스택을 실행한 상태로 순차 실행한다. 기존 화면 공유 회귀는 옵션 없이 실행한다. 실제 장치 권한을 요청하는 검사가 아니며, 실제 iPhone·외부망 인수를 대신하지 않는다. [독립 결과와 제한](../../docs/verification/05a-mobile-single-rendition.md)을 따른다.

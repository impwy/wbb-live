# 07-A2 제품 통합 격리 검증 환경

이 Compose는 패치된 MediaMTX와 제품 API·worker·Flutter Web을 별도 loopback 포트에서 연결한다. 기존 07-A1 실험과 로컬 미디어 스택의 컨테이너·볼륨·포트는 사용하지 않는다. 운영 배포 구성은 아니다.

## 처음 준비

`env.example`을 참고해 비추적 `.env`를 생성한다. `WBB_MEDIA_SECRET`은 기존 create/worker/control Basic 인증용 ASCII 문자열이고, `WBB_COMPLETION_KEY_HEX`는 별도의 32바이트 HMAC 키의 hex 표현이다. 서로 재사용하지 않는다. 인증값을 문서·로그에 기록하지 않는다.

명시적 anchor JSON은 `{"root":"<고정-root>","slot":"<고정-slot>"}` 형식이다. 두 값은 `.env`의 ROOT/SLOT과 정확히 같아야 한다. `WBB_COMPLETION_ANCHOR_FILE`에는 해당 파일의 로컬 절대 경로를 둔다. 기존 볼륨을 비우거나 부팅 중 자동 초기화하지 않는다.

```sh
docker compose -p wbb-media-completion-integration --env-file .env -f compose.yml build mediamtx api worker
docker volume create wbb-media-completion-integration_completion-state
docker compose -p wbb-media-completion-integration --env-file .env -f compose.yml \
  run --rm --no-deps --entrypoint /wbb-bootstrap mediamtx /dev/null
docker compose -p wbb-media-completion-integration --env-file .env -f compose.yml up -d
```

위 bootstrap은 **처음 만든 빈 전용 볼륨에 한 번만** 실행한다. 이후에는 `up`만 실행한다. Compose가 `.env`의 로컬 anchor 경로를 bind mount에 사용한다.

HTTPS는 `https://localhost:19543`, PostgreSQL은 `127.0.0.1:55439`, MediaMTX 관리 조회는 `127.0.0.1:29998`, ICE는 `127.0.0.1:19991/udp`다. 관리 주소는 loopback 검증 전용이며 공개 적용안이 아니다. Caddy의 내부 인증서는 검증용 Chrome 프로필 또는 인증서 pin으로만 신뢰한다.

종료 시 `docker compose -p wbb-media-completion-integration --env-file .env -f compose.yml down`을 사용한다. 데이터 볼륨은 별도 명시 작업 없이 삭제하지 않는다. `down -v`를 사용하지 않는다.

현재 검증 상태는 실행 결과와 분리한다. API/worker/브라우저의 실제 완료 증거가 통과하기 전에는 이 구성을 제품 완료로 해석하지 않는다.

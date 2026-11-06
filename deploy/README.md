# WBB 배포 구성 (스캐폴드)

상태: 자리표시자. 실제 배포는 티켓 09(deployment-and-acceptance)에서 구성하며,
비밀 값은 절대 이 디렉터리에 커밋하지 않는다(자리표시자만).

## 목표 구성 (docs/design.md §2, §11)

- Caddy: 유일한 공개 진입점. `live.wombatburrow.com` TLS, `/api/v1` → Go API,
  `/media` → MediaMTX LL-HLS, 나머지 → Flutter Web 정적 파일.
- k3s 내 WBB 전용 namespace/Secret/PVC. 기존 서비스·인증서는 변경하지 않는다.
- MediaMTX(WHIP ingest) + FFmpeg(Intel VAAPI 1080/720/480)는 후속 티켓(02~04).
- 관리형 인터페이스(DB/Redis/Kafka/MediaMTX API/Grafana)은 공개하지 않는다.

## 현재까지 확정된 것

- API 컨테이너: `wbb-backend/Dockerfile` (go1.27.3-alpine 빌드, Alpine 런타임(nonroot)).
- 개발 스택: 루트 `compose.yaml` (postgres/redis/api, 127.0.0.1 바인딩만).
- Flutter Web 빌드는 `--no-web-resources-cdn`로 CanvasKit을 로컬에서 제공한다(CDN 미사용 요건).

## 추가 예정

- `Caddyfile`: live.wombatburrow.com 라우팅 예시(자리표시자 도메인만).
- k3s manifests: api/web/mediamtx/ffmpeg-worker/observability.
- 백업·복원 절차(PostgreSQL 볼륨).

# WBB 배포 구성

운영 구성은 `deploy/live/`의 Docker Compose 프로젝트 `wbb-live`다. 비밀 값은 서버의 `.env`(권한 600)에만 두고 저장소에는 자리표시자(`env.example`)만 둔다.

## 구성

| 서비스 | 역할 | 외부 노출 |
| --- | --- | --- |
| `edge` (Caddy) | `/` Flutter 정적 파일, `/api/v1` → API, `/media` → API 인가 후 MediaMTX LL-HLS | `127.0.0.1:19480`만 (호스트 Caddy가 TLS 종료 후 프록시) |
| `api` | Go API (`WBB_ENV=prod`) | 없음 |
| `worker` | 송출 감지·FFmpeg 720p 변환·종료 정리 | 없음 |
| `mediamtx` | WHIP 수신(API 중계), LL-HLS, 종료 근거 기록 패치 | ICE `UDP/TCP 18391`만 |
| `postgres`, `redis` | 원장 / 세션 캐시·요청 한도 | 없음 |

호스트 Caddy에는 `live.caddy` 사이트 하나만 `conf.d`에 추가한다. 기존 사이트·인증서·allowlist는 바꾸지 않는다. live DNS는 Cloudflare 프록시를 거치지 않는 DNS only 레코드다(CDN 미사용, WebRTC 직접 연결).

## 최초 배포

```sh
cd deploy/live
./scripts.sh init-secrets     # .env 생성, WBB_ICE_HOSTS=<공인IP>,<LAN IP> 편집
cp -r <flutter build/web> web # 정적 파일
./scripts.sh bootstrap        # 이미지 빌드 + 빈 완료 이력 볼륨 1회 초기화 (재실행 거부)
./scripts.sh up
# 평가 계정: 일반 가입 API로 생성 후 .env의 WBB_EVAL_LOGIN_* 세 값을 채우고 ./scripts.sh up
# 호스트 Caddy: conf.d에 live.caddy 추가 → caddy validate → caddy reload
```

공유기에서 TCP 80/443(기존)과 ICE `UDP/TCP 18391`을 서버로 전달해야 외부망 송출이 된다. 이 포트가 없으면 시청·로그인은 되지만 송출은 서버와 같은 LAN에서만 가능하다.

## 운영 주의

- `completion-state` 볼륨(외부 볼륨 `wbb-live_completion-state`)과 `pg-data`는 롤백 시에도 지우지 않는다. `down -v`를 사용하지 않는다.
- 업데이트: 새 소스·정적 파일 배치 후 `./scripts.sh up` (bootstrap은 다시 실행하지 않음).
- 되돌리기: `conf.d`의 live 파일만 제거하고 Caddy reload, `./scripts.sh down`.
- 프록시 뒤에서 API의 IP별 요청 한도는 전체 공용 한도로 동작한다. 기본값은 `compose.yml`에서 상향했다.

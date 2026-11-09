# WBB (wombatburrow)

치지직 스타일의 실시간 라이브 방송 플랫폼입니다. 방송자는 PC 화면(공유 오디오+마이크) 또는 휴대폰 카메라·마이크로 송출하고, 시청자는 로그인 없이 웹에서 시청합니다.

- **상태**: 티켓01 이메일 인증·방송 CRUD 구현 및 검증 완료. 티켓02 로컬 합성 미디어 검증83항목 통과(실제 기기·외부망 검증은 미완료). VAAPI 후속은 독립 브라우저13/13·해상도3종·종료 검증 통과. Google 로그인은 구현·자동 검증을 통과했으며 실제 Google 인증은 미검증. 04-A 로컬 회차 준비·취소와 영속 상태는 독립 검증·CI를 통과했습니다. 제품 송출·시청 통합은04-B에서 진행 중입니다. [필수 요구사항 현황](docs/verification/required-implementation-status.md)을 참고하세요.
- **서비스 URL**: 아직 미배포. 운영 목표는 `https://live.wombatburrow.com`(티켓 09).
- **평가 로그인**: 별도 계정 없음. 현재는 로컬 실행 후 화면의 가입 절차로 직접 계정을 만들어 사용한다.
- 설계 계약: `docs/design.md` (Codex 관리)
- 요구사항: `requirements/` (원본 보존)
- 용어: `GLOSSARY.md`

## 구성

| 경로 | 내용 |
| --- | --- |
| `wbb-backend/` | Go API 서버 (`net/http` + pgx + go-redis, Argon2id, HS256 JWT) |
| `wbb-frontend/` | Flutter Web 앱 (방송 목록/상세, 가입·로그인, 내 방송 CRUD) |
| `experiments/media-single-rendition/` | 로컬 미디어 실험. [실행 조건과 알려진 실패](experiments/media-single-rendition/README.md) |
| `experiments/media-vaapi/` | GPU 후속 실험. [실행·미완료 사항](experiments/media-vaapi/README.md), [독립 검증](docs/verification/02-media-vaapi.md) |
| `deploy/` | 배포 구성 (Caddy/k3s, 추후 확장) |
| `docs/`, `.scratch/` | 설계 문서와 티켓 (Codex 관리) |

데이터 저장: PostgreSQL(원장) + Redis(세션 활성 sid 캐시·rate limit). Kafka·미디어 스택은 후속 단계에서 추가됩니다.

## 사전 요구

- Go 1.27.3+
- Flutter 3.47.x (stable)
- Docker & Docker Compose (PostgreSQL/Redis 실행용)

## 빠른 시작

```bash
make deps-up          # PostgreSQL(55432) + Redis(56379) 기동 (127.0.0.1에만 바인딩)
make run-backend      # API 서버 기동 (http://localhost:8080)

# Flutter 개발 서버 (API를 http://localhost:8080로 지정)
make frontend-run     # http://localhost:3000

# 또는: 정적 웹 번들을 빌드해 API가 직접 제공
make frontend-build   # wbb-frontend/build/web 생성
WBB_STATIC_DIR=../wbb-frontend/build/web make run-backend
```

전체 백엔드 스택(API+PostgreSQL+Redis)은 `docker compose up --build`로 띄울 수 있습니다.
Flutter 웹 번들은 compose에 포함되지 않으며 `make frontend-build`로 별도 빌드해
`WBB_STATIC_DIR` 또는 Caddy 등의 정적 서버로 제공합니다.

### 환경변수

`.env.example` 참고. 비밀값은 항상 환경변수로만 전달하고 저장소에 넣지 않습니다.
개발용 compose의 DB 비밀번호(`wbb-dev-password`)는 로컬 전용 자리표시자입니다.

- `WBB_DATABASE_URL`(필수), `WBB_REDIS_ADDR`
- `WBB_JWT_SECRET`(prod 필수, 32바이트 이상). dev에서 미설정 시 프로세스마다 임시 키 생성 → 기존 access JWT는 무효가 되지만, 불투명 refresh 토큰과 PostgreSQL 세션은 원칙적으로 유지되어 refresh로 재로그인 없이 복구된다
- `WBB_CORS_ORIGINS`: auth POST(가입/로그인/refresh/logout)에 허용되는 정확한 Origin 목록. 운영은 `https://live.wombatburrow.com`
- Google 선택 설정: `WBB_GOOGLE_CLIENT_ID`, `WBB_GOOGLE_CLIENT_SECRET`, `WBB_GOOGLE_CALLBACK_URI`를 모두 설정한다. 전체 미설정은 기능 비활성화, 부분 설정은 시작 실패다. 실제 값은 환경변수로만 전달한다.
- `WBB_ACCESS_TOKEN_TTL`(기본 10m), `WBB_REFRESH_TOKEN_TTL`(기본 168h)
- `WBB_RATE_LIMIT_REQUESTS`(IP별, 기본 30/분), `WBB_RATE_LIMIT_ACCOUNT_REQUESTS`(계정별, 기본 10/분)

### Google 로그인 확인 (실제 Google 인증 미검증)

실제 Google 인증 검증에는 Google Console에서 발급한 Web client와 정확히 등록한 callback이 필요하다. 로컬 예시는 `http://localhost:8080/api/v1/auth/google/callback`, 운영 목표는 `https://live.wombatburrow.com/api/v1/auth/google/callback`이다. 개발 HTTP는 loopback만 허용하고 운영은 HTTPS를 사용한다. 실제 client secret은 Git에 저장하지 않는다.

```bash
# 아래 값은 설명용 자리표시자다. 실제 자격은 로컬 환경에서 주입한다.
export WBB_GOOGLE_CLIENT_ID='<google-web-client-id>'
export WBB_GOOGLE_CLIENT_SECRET='<google-web-client-secret>'
export WBB_GOOGLE_CALLBACK_URI='http://localhost:8080/api/v1/auth/google/callback'
make frontend-build
WBB_STATIC_DIR=../wbb-frontend/build/web make run-backend
```

`http://localhost:8080`에서 가입/로그인 화면의 Google 버튼으로 시작한다. API와 Flutter를 같은 출처에서 제공해야 `/auth/google/complete`의 직접 진입·새로고침이 연결된다. 분리된 개발 서버3000만 실행한 상태는 이 확인 환경이 아니다. 정상 로그인 후 내 방송 등록·새로고침·로그아웃, 취소 후 재시도, 같은 이메일 계정 충돌 안내를 확인한다. 버튼은 서버의 `/api/v1/auth/providers`가 Google 활성화를 알릴 때 표시된다. 모의 provider 검사 성공을 실제 Google 로그인 성공으로 취급하지 않는다.

## 로컬 방송 회차 준비·취소

가입·로그인 후 내 방송을 등록하고 상세 화면에서 **방송 회차 준비**를 누릅니다. 준비 중에는 **준비 취소**로 종료를 요청할 수 있고, 별도 비로그인 화면에서도 상태를 조회할 수 있습니다. 준비가60초를 넘으면 서버의 정리 작업이 종료 처리합니다. 이 단계는 화면·카메라 캡처나 영상 송출을 시작하지 않습니다. 실제 송출·재생 UI는04-B 후속입니다.

## API 요약 (`/api/v1`)

| 메서드/경로 | 권한 | 비고 |
| --- | --- | --- |
| POST `/auth/register` | 공개(Origin 검증) | 201 `{user}`, 자동 로그인 없음 |
| POST `/auth/login` | 공개(Origin 검증) | 200 `{accessToken,user}` + HttpOnly refresh 쿠키(회전) |
| POST `/auth/refresh` | refresh 쿠키(Origin 검증) | 회전. 재사용 감지 시 family 전체 폐기 |
| POST `/auth/logout` | refresh 쿠키 또는 access JWT | 204 멱등. 두 자격 모두 독립 폐기 |
| GET `/auth/me` | access JWT | 200 `{user}` |
| GET `/auth/providers` | 공개 | `{googleEnabled}` |
| GET `/auth/google/start` | 공개 | state·nonce·PKCE 생성, Google로 이동 |
| GET `/auth/google/callback` | state·브라우저 결합 검증 | 토큰 교환·계정 확인 후 고정 완료 화면으로303 |
| GET `/broadcasts` | 공개 | `{items,nextCursor}` cursor 페이지네이션(기본 20/최대 50) |
| POST `/broadcasts` | 로그인 | 201. `ownerId`는 서버가 결정 |
| GET `/broadcasts/{id}` | 공개 | 404 포함 |
| PATCH `/broadcasts/{id}` | 소유자 | `version` 조건부 갱신, 충돌 409 |
| DELETE `/broadcasts/{id}?version=N` | 소유자 | soft delete, 204; 활성 회차가 있으면409 |
| POST `/broadcasts/{id}/sessions` | 소유자 | source, Idempotency-Key;202 준비 회차·단기 grant |
| GET `/broadcasts/{id}/sessions/current` | 공개 | 현재/최근 회차 또는 null; 민감 정보 제외 |
| POST `/broadcasts/{id}/sessions/{sid}/stop` | 소유자 |202 종료 처리, 이미 종료면200 |
| POST `/broadcasts/{id}/sessions/{sid}/publish-grants` | 원래 로그인 family의 소유자 | 조건을 만족하는 준비 회차의 만료 grant 갱신 |
| GET `/health/live`, `/health/ready` | 공개/내부 | 프로세스 생존 / DB·마이그레이션 준비 |

오류 형식: `{"error":{"code","message","requestId"}}`. 인증 세션의 최종 원장은 PostgreSQL `auth_session_families`/`auth_sessions`이며 Redis는 캐시일 뿐(장애 시 보호 API 503 fail-closed, 누락 시 원장으로 자가 복구).

## 테스트

테스트 경계: 공개 HTTP API(권한·상태), 실제 PostgreSQL 영속성, Flutter 사용자 동작.

```bash
make test-backend      # 실제 PG 대상 통합 테스트 (-race). Redis는 실행마다 disposable 컨테이너로 격리
make vet-backend
make frontend-analyze  # flutter analyze
make frontend-test     # 위젯/API 클라이언트 테스트
make frontend-build    # web release 빌드 (--no-web-resources-cdn, CanvasKit 로컬 제공)
make ci                # 위 전체
```

CI(GitHub Actions, `.github/workflows/ci.yml`)도 동일하게 backend(`go vet/test -race/govulncheck`)와 frontend(`analyze/test/build`)를 실행합니다.

## 보안 노트

- 비밀번호 Argon2id(PHC 형식 저장), access JWT는 브라우저 메모리에만 보관(localStorage 미사용).
- refresh 토큰은 `Secure; HttpOnly; SameSite=Lax; Path=/api/v1/auth` 쿠키로만 전달, 회전 주기마다 갱신.
- 가입·로그인·refresh·logout 모두 Origin 정확 일치 검증. IP/계정별 rate limit은 가입·로그인·refresh에 적용(logout은 제외).
- 로그·응답에 비밀번호·토큰·SQL·DSN을 남기지 않습니다(테스트 오류 메시지도 DSN 마스킹).

## 배포 (예정)

`deploy/` 참고. Labtop 서버의 k3s, Caddy(`live.wombatburrow.com`), MediaMTX/FFmpeg, 전용 LGTM 스택은 후속 티켓에서 구성합니다. CDN은 사용하지 않습니다.

## 라이선스/출처

- 백엔드: Go 표준 라이브러리, [pgx](https://github.com/jackc/pgx), [go-redis](https://github.com/redis/go-redis), [jwt](https://github.com/golang-jwt/jwt), [x/crypto](https://pkg.go.dev/golang.org/x/crypto) (Argon2id)
- 프런트엔드: Flutter, [http](https://pub.dev/packages/http), [provider](https://pub.dev/packages/provider)
- 한글 폰트: [Noto Sans KR](https://github.com/google/fonts/tree/main/ofl/notosanskr) (SIL OFL 1.1, 라이선스 전문 `wbb-frontend/assets/fonts/OFL.txt`) — 폰트를 번들해 런타임 CDN 요청이 없다. 번들 폰트에 없는 글리프(예: 일부 이모지)는 로컬 폴백 경로(`/assets/fonts/fallback/`)로만 해석되며 미지원 글리프로 표시될 수 있다.

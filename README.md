# WBB Live (wombatburrow)

치지직 스타일의 실시간 라이브 방송 웹 서비스입니다. 방송자는 **PC 화면 + 탭/시스템 소리 + 마이크** 또는 **휴대폰 카메라 + 마이크**로 송출하고, 시청자는 로그인 없이 웹 브라우저에서 시청합니다.

| 항목 | 내용 |
| --- | --- |
| 서비스 주소 | **https://live.wombatburrow.com** |
| 테스트 로그인 | 로그인 화면의 **「평가용 바로 로그인」** 버튼 (비밀번호 입력 없음). 직접 회원가입도 가능 |
| 지원 브라우저 | PC 송출: Chrome·Edge 최신 버전(탭/시스템 소리 공유는 Chromium 계열 기능) / 모바일 송출: 카메라·마이크 권한을 지원하는 최신 모바일 Chrome·Safari / 시청: 최신 PC·모바일 브라우저 |
| 기술 스택 | Go API · Flutter Web · PostgreSQL · Redis · MediaMTX(WebRTC WHIP 수신, LL-HLS 배포) · FFmpeg |

## 평가 체크리스트 대응

| ID | 구현 위치 | 자동 테스트 (공개 HTTP + 실제 PostgreSQL) | 공개 서비스 확인 |
| --- | --- | --- | --- |
| BASE-01 | 전체 | CI: `go vet`, `go test -race ./...`, `flutter analyze`, `flutter test`, `flutter build web` | ✅ |
| PC-01~04 | `wbb-frontend/web/wbb_media.js`(화면·탭 소리·마이크 혼합, WHIP), `internal/mediaworker`(변환·종료 정리) | `media_test.go`, `playback_test.go`, Flutter `live_detail_test.dart` | ✅ 합성 장치 송출·종료 (실제 PC 캡처는 수동 확인 대상) |
| MOB-01~03 | `wbb_media.js` 카메라·마이크 경로, `lib/screens/broadcast_detail_screen.dart` | Flutter `live_detail_test.dart`(카메라 시작·숨김 종료) | ✅ 실제 휴대폰(LTE) 송출 → 다른 기기 시청 → 종료 |
| BCAST-01 | `internal/broadcasts`, `lib/screens/broadcast_form_screen.dart` | `TestBroadcastLifecycle`, `TestBroadcastValidation`, `TestBroadcastVersionConflict` | ✅ |
| BCAST-02 | `lib/screens/home_screen.dart`(목록·방송 중 필터), `broadcast_detail_screen.dart`(LL-HLS 재생) | `TestBroadcastPagination`, `TestPlaybackAndEveryHLSObjectStopAtSessionEnd` | ✅ 다른 회선 익명 재생 |
| BCAST-03 | `internal/livesessions`, `internal/mediaworker` | `sessions_test.go`, `TestPlaybackAndEveryHLSObjectStopAtSessionEnd` | ✅ 종료 후 재생 409 |
| BCAST-04 | PostgreSQL 마이그레이션 `0003`·`0005` | `TestPersistenceAcrossServerRestart` | ✅ |
| AUTH-01 | `internal/auth`, `lib/state/auth_controller.dart` | `auth_flow_test.go`(가입·로그인·refresh 회전·로그아웃), `evaluation_test.go` | ✅ 평가용 바로 로그인 |
| AUTH-02 | `requireAuth` 미들웨어, 공개 목록·재생 | `TestBroadcastCreateRequiresAuth`, `TestWHIPRejectsUnauthorisedPublication` | ✅ |
| AUTH-03 | `broadcasts.checkOwner`, `livesessions` 소유자 검사 | `TestBroadcastOwnershipMatrix`, `TestEvaluationLoginPublicAuthAndOwnership` | ✅ |
| COMMENT-01·02 | `internal/comments`, `lib/screens/comments_panel.dart` | `TestCommentsCRUDAndAuthorAuthorization`(타인 403·비로그인 401) | ✅ |
| DONATION-01~03 | `internal/donations`(서버 내장 테스트 결제 게이트웨이), `lib/screens/donations_panel.dart` | `TestDonationsVerifyApprovalAndCreditOnce`(위조 금액·타인 거절, 중복 승인 1회 적립, 합계) | ✅ |
| VOD-01~03 | `internal/mediaworker`(MP4 녹화), `internal/recordings`, `lib/screens/recording_screen.dart` | `TestRecordingsRegisterListAndServe`(위치·메타데이터 저장, 재시작 후 조회, Range 재생) | ✅ |

선택 기능 상세 근거: [docs/verification/optional-features.md](docs/verification/optional-features.md). 필수 기능 진행 기록: [docs/verification/required-implementation-status.md](docs/verification/required-implementation-status.md).

## 평가용 로그인

1. https://live.wombatburrow.com 접속 → 오른쪽 위 **로그인**.
2. **평가용 바로 로그인**을 누르면 서버에 미리 준비한 평가 전용 계정으로 로그인됩니다.
3. 평가 계정은 여러 심사자가 공유합니다. 같은 평가 계정으로 로그인한 사람끼리는 서로의 방송을 수정·삭제·중지할 수 있습니다. 소유자 권한을 확인하려면 **회원가입**으로 두 번째 계정을 만들어 비교해 주세요(아래 "권한 확인").

평가 로그인은 서버 환경변수로 켤 때만 버튼과 API(`POST /api/v1/auth/evaluation-login`)가 활성화됩니다. 실제 계정 비밀번호는 서버 비밀 설정에만 있고 저장소·응답·로그에 없습니다.

## 기능 확인 절차

### 방송 등록·수정·삭제 (BCAST-01)

1. 로그인 후 목록 화면의 **방송 등록**(오른쪽 아래) 또는 상단 **내 방송** → **+** 를 누릅니다.
2. 제목(1~100자)과 설명을 입력해 저장하면 목록에 나타납니다.
3. 방송 상세 화면 오른쪽 위의 ✏️(수정)·🗑️(삭제), 또는 **내 방송**의 메뉴에서 수정·삭제합니다. 방송 중에는 삭제할 수 없습니다(먼저 중지).

### PC 화면 방송 (PC-01~04)

1. PC Chrome/Edge에서 로그인 → 내 방송의 상세 화면으로 이동합니다.
2. **화면 공유 시작**을 누르고, 브라우저 공유 창에서 **Chrome 탭**(또는 전체 화면)을 고른 뒤 **탭 오디오 공유(시스템 오디오 공유)** 를 켭니다. 이어서 마이크 권한을 허용합니다.
3. 상태가 `송출: 송출 중`, 방송 상태가 **LIVE**가 되면 공유한 화면·소리와 마이크 음성이 함께 송출됩니다. 공유 소리가 없으면 화면에 안내가 표시됩니다.
4. **방송 중지** 또는 브라우저의 **공유 중지**를 누르면 송출 연결이 닫히고 상태가 **종료**로 바뀝니다. 종료 후에는 이전 시청 URL로 재생할 수 없습니다.

### 휴대폰 카메라 방송 (MOB-01~03)

1. 휴대폰 브라우저에서 로그인 → 내 방송 상세 화면에서 **카메라·마이크 시작**을 누르고 카메라·마이크 권한을 허용합니다.
2. 미리보기가 나오고 **LIVE**가 되면 영상과 음성이 송출됩니다.
3. **방송 중지**를 누르면 종료됩니다. 송출 중 화면을 끄거나 다른 앱으로 전환하면 방송이 자동 종료되며, 돌아와도 자동으로 다시 송출하지 않습니다.

### 시청 (BCAST-02, AUTH-02)

1. **다른 기기**(로그인 불필요)에서 https://live.wombatburrow.com 접속 → 목록의 **방송 중** 필터 또는 **LIVE** 표시 카드를 선택합니다.
2. 영상이 준비되면 **재생**, 소리는 **소리 켜기**를 누릅니다(브라우저 자동재생 정책 때문에 소리는 직접 켜야 합니다). 지연이 쌓이면 **라이브로 이동**을 누릅니다.
3. 방송자가 중지하면 시청 화면이 "방송이 종료되었습니다."로 바뀝니다. 목록은 10초, 상세는 5초마다 상태를 갱신합니다.

### 권한 확인 (AUTH-01~03)

| 사용자 | 목록·상세·시청 | 방송 등록·송출 | 남의 방송 수정·삭제·송출 제어 |
| --- | --- | --- | --- |
| 비로그인 | 허용 | 거절(로그인 화면으로 이동, API 401) | 거절(401) |
| 방송 소유자 | 허용 | 허용 | — (자기 방송은 허용) |
| 다른 사용자 | 허용 | 자기 방송만 허용 | 거절(서버 403, 화면에 버튼 없음) |

서버는 모든 수정·삭제·송출 시작/중지·송출 권한 갱신 요청에서 인증 세션과 방송 소유자를 확인합니다. 예: 다른 계정의 access token으로 `PATCH /api/v1/broadcasts/{id}` → `403 forbidden`.

### 영속성 (BCAST-04)

방송 정보·회차·종료 상태는 PostgreSQL에 저장됩니다. API·DB 컨테이너를 재시작해도 목록·상세·종료 상태가 그대로 조회됩니다.

### 추가 기능: 라이브 채팅

1. 방송 상세에서 PC는 재생 화면 오른쪽, 휴대폰은 재생 버튼 아래에 **채팅** 창이 있습니다. 로그인 없이 실시간으로 읽고, 로그인하면 보낼 수 있습니다(200자, 너무 빠른 연속 전송은 429).
2. 서버는 Server-Sent Events(`GET /api/v1/broadcasts/{id}/chat/stream`)로 최근 50개와 새 메시지를 밀어 주고, Redis pub/sub로 API 인스턴스 간에 전달합니다. 채팅은 휘발성이며 최근 기록은 24시간만 보관합니다(영구 기록이 필요한 글은 댓글 사용).

### 선택 기능: 댓글 (COMMENT-01·02)

1. 방송 상세 화면 아래 **댓글**에서 로그인한 사용자가 댓글을 작성합니다. 목록은 로그인 없이 볼 수 있고 5초마다 갱신됩니다.
2. 내 댓글의 ⋮ 메뉴로 **수정·삭제**합니다. 다른 사람 댓글에는 메뉴가 없으며, 서버도 작성자가 아니면 `403`으로 거절합니다(`PATCH/DELETE /api/v1/comments/{id}`).

### 선택 기능: 도네이션 (DONATION-01~03)

결제는 **서버에 내장한 테스트 결제 모듈**(메모리 기반 테스트 게이트웨이)로 진행하며 실제 금액은 청구되지 않습니다.

1. 로그인 후 방송 상세의 **후원하기**에서 금액(1,000~1,000,000원)과 메시지를 고릅니다. 서버가 후원 대상(방송 소유자)과 금액을 고정한 주문을 PostgreSQL에 만듭니다.
2. **테스트 결제창**에서 **결제 승인**(또는 결제 실패 처리)을 누르면 테스트 게이트웨이가 저장된 주문 금액으로 결제 키를 발급합니다. 클라이언트가 보낸 금액은 사용하지 않습니다.
3. 서버가 게이트웨이에 승인을 요청하고, 결과(`DONE`, 주문번호, 금액)를 주문과 대조한 뒤에만 후원을 확정합니다. 금액·주문·후원자가 다르면 거절합니다.
4. 완료 창의 **승인 통지 다시 보내기**로 같은 승인 통지를 반복해도 주문 단위로 한 번만 적립되는 것을 확인할 수 있습니다(`POST /donations/confirm` 반복, `POST /payments/webhook` 재전송 모두 멱등).
5. 방송 상세에 방송별 후원 내역과 합계가 표시됩니다.

### 선택 기능: 녹화·다시보기 (VOD-01~03)

1. 방송하면 서버의 720p 변환기가 라이브 송출과 동시에 같은 영상을 MP4로 저장합니다(저장 실패가 라이브를 끊지 않음).
2. 방송이 끝나면 서버가 녹화 정보(방송·회차·저장 위치·크기·시작 시각·길이)를 PostgreSQL에 등록합니다. 재시작 후에도 조회됩니다.
3. 홈의 **다시보기** 필터에서 녹화 목록을, 방송 상세의 **다시보기** 목록에서 해당 방송의 녹화를 열어 재생합니다(`GET /api/v1/recordings`, `/api/v1/recordings/{id}/media`, Range 지원).

## 현재 공개 환경의 제약

- **외부망(다른 회선)에서의 송출**은 서버의 WebRTC ICE 포트(UDP/TCP 18391)로 직접 연결하거나, 포트가 막힌 경우 Cloudflare TURN 중계를 거칩니다. 중계 시 지연이 약간 늘 수 있습니다. 시청·로그인·방송 관리는 HTTPS만 사용합니다. 현재 상태는 아래 "공개 배포 상태"에 기록합니다.
- 화질은 단일 720p(H.264)입니다. 다중 화질(ABR)은 구현하지 않았습니다.
- 프록시 뒤에서는 클라이언트 IP별이 아닌 전체 공용 요청 한도로 동작합니다.

### 공개 배포 상태

2026-11-12 공개 URL에서 확인한 결과입니다. 서버와 **다른 인터넷 회선**의 PC에서 실행했습니다.

| 확인 항목 | 결과 |
| --- | --- |
| 공개 HTTPS(신뢰 인증서)·화면·API | ✅ 정상 |
| 평가용 바로 로그인, 잘못된 Origin 거절(403) | ✅ 정상 |
| 방송 등록 → 카메라·마이크 WHIP 송출 → LIVE 전환 | ✅ 약 9초 내 LIVE (Chrome, 합성 카메라·마이크) |
| 로그인하지 않은 별도 Chrome에서 LL-HLS 재생 | ✅ 재생 시간 지속 증가, 멈춤 0회 |
| 방송 중지 → 종료 상태 전환, 종료 후 재생 거절 | ✅ 약 1초 내 `ended`, 재생 요청 409 |
| 인가 없는 HLS 요청 | ✅ 403 |
| 실제 휴대폰(LTE) 카메라·마이크 송출 → 다른 기기 시청 → 종료 | ✅ 정상 (2026-11-12 실제 기기). 마이크 샘플이 없을 때 "준비 중"에서 멈추던 문제와 약 15초 후 변환이 끊기던 문제를 수정한 뒤 확인 |

자동 점검 스크립트: `node deploy/live/verify-public.mjs https://live.wombatburrow.com` (송출용 Chrome은 합성 장치를 사용하므로 실제 기기 확인과 구분합니다).

## 직접 실행하기

### 전체 스택 (송출·시청 포함, Docker)

필요: Docker + Docker Compose, Flutter 3.47.x(웹 빌드용).

```bash
git clone https://github.com/impwy/wbb-live.git && cd wbb-live
(cd wbb-frontend && flutter build web --release --no-web-resources-cdn)
cp -r wbb-frontend/build/web deploy/live/web
cd deploy/live
./scripts.sh init-secrets          # 무작위 비밀값으로 .env 생성 (권한 600)
# .env 편집: 로컬에서는 아래처럼 바꿉니다.
#   WBB_PUBLIC_ORIGIN=http://localhost:19480
#   WBB_ICE_HOSTS=127.0.0.1
./scripts.sh bootstrap             # 이미지 빌드 + 완료 이력 볼륨 최초 1회 초기화
./scripts.sh up                    # 기동
```

브라우저에서 http://localhost:19480 을 엽니다(localhost는 보안 컨텍스트라 화면 공유·카메라가 동작합니다). 다른 기기에서 접속하려면 HTTPS가 필요하므로 아래 공개 배포 방식을 사용합니다.

평가용 바로 로그인을 켜려면 일반 가입으로 계정을 만든 뒤 `.env`에 세 값을 모두 넣고 `./scripts.sh up`을 다시 실행합니다.

```bash
WBB_EVAL_LOGIN_ENABLED=true
WBB_EVAL_LOGIN_EMAIL=<evaluation-account-email>
WBB_EVAL_LOGIN_PASSWORD=<evaluation-account-password>
```

### 공개 배포 (live.wombatburrow.com)

`deploy/live/` 구성을 서버에서 위와 같이 실행하고(`WBB_PUBLIC_ORIGIN=https://live.wombatburrow.com`, `WBB_ICE_HOSTS=<공인IP>,<LAN IP>`), 호스트 Caddy에 `deploy/live/live.caddy` 사이트를 추가해 TLS를 종료합니다. 공유기에서 TCP 80/443과 ICE 포트 UDP/TCP 18391을 서버로 전달합니다. DB·Redis·MediaMTX 관리 API는 외부에 열지 않습니다. 자세한 구성은 [deploy/README.md](deploy/README.md)를 참고하세요.

### 개발용 (API·화면만)

```bash
make deps-up          # PostgreSQL(55432) + Redis(56379), 127.0.0.1 바인딩
make run-backend      # API http://localhost:8080
make frontend-run     # Flutter 개발 서버 http://localhost:3000
make test-backend     # 실제 PostgreSQL 대상 통합 테스트(-race)
make ci               # vet, test, flutter analyze/test/build
```

### 주요 환경변수

| 변수 | 설명 |
| --- | --- |
| `WBB_DATABASE_URL` | PostgreSQL 접속 URL (필수) |
| `WBB_REDIS_ADDR` | Redis 주소 (세션 캐시·요청 한도) |
| `WBB_JWT_SECRET` | access JWT 서명 키, 운영 필수(32바이트 이상) |
| `WBB_CORS_ORIGINS` | 인증 POST에 허용할 정확한 Origin (예: `https://live.wombatburrow.com`) |
| `WBB_MEDIA_SECRET`, `WBB_COMPLETION_*` | API·worker·MediaMTX 간 내부 인증과 송출 종료 근거 계약 |
| `WBB_EVAL_LOGIN_ENABLED/EMAIL/PASSWORD` | 평가용 바로 로그인 (세 값 모두 설정 또는 모두 미설정) |
| `WBB_GOOGLE_CLIENT_ID/SECRET/CALLBACK_URI` | Google 로그인(선택, 세 값 모두 필요) |
| `WBB_ICE_HOSTS`, `WBB_ICE_PORT` | 브라우저에 알릴 WebRTC 후보 주소와 포트 (deploy/live) |
| `WBB_RECORDINGS_DIR` | 선택. 녹화 저장 경로(worker 쓰기, API 읽기 전용). 미설정 시 녹화·다시보기 비활성 |
| `WBB_TURN_KEY_ID`, `WBB_TURN_API_TOKEN` | 선택. Cloudflare TURN 키. 설정하면 방송자 브라우저가 단기 TURN 자격(`GET /api/v1/media/ice-servers`, 로그인 필요)으로 미디어를 중계해, ICE 포트를 열 수 없는 환경에서도 송출됩니다 |

예시 값은 `deploy/live/env.example`, `.env.example`에 자리표시자로만 있습니다. 실제 비밀값은 저장소에 넣지 않습니다.

## 구조

```
브라우저 ──HTTPS──▶ Caddy ─┬─ /            Flutter Web 정적 파일
                           ├─ /api/v1/*     Go API ── PostgreSQL / Redis
                           └─ /media/*      (API 인가 후) MediaMTX LL-HLS
방송자 브라우저 ──WHIP(API 중계)──▶ MediaMTX ◀──ICE UDP/TCP 18391── WebRTC 미디어
media worker: 송출 감지 → FFmpeg 720p 변환 → LL-HLS, 종료 시 연결·변환 정리
```

| 경로 | 내용 |
| --- | --- |
| `wbb-backend/` | Go API(`cmd/wbb-api`)와 미디어 worker(`cmd/wbb-media-worker`) |
| `wbb-frontend/` | Flutter Web 앱 (목록·상세·시청, 가입·로그인, 내 방송, 송출) |
| `deploy/live/` | 운영 Docker Compose, 내부 Caddy, MediaMTX 설정 |
| `experiments/` | 미디어 검증 스택과 MediaMTX 종료 근거 패치(`experiments/media-completion`) |
| `docs/`, `.scratch/` | 설계(`docs/design.md`), 검증 기록, 티켓 |

방송 상태: `ready`(대기) → `starting`(준비 중) → `live`(방송 중) → `stopping`(종료 중) → `ended`(종료). 종료는 MediaMTX가 송출 연결 종료를 기록한 근거를 서버가 확인한 뒤에 확정하며, 그때 변환 프로세스와 HLS 접근도 정리됩니다.

## API 요약 (`/api/v1`)

| 메서드/경로 | 권한 | 비고 |
| --- | --- | --- |
| POST `/auth/register` | 공개(Origin 검증) | 201 `{user}`, 자동 로그인 없음 |
| POST `/auth/login` | 공개(Origin 검증) | 200 `{accessToken,user}` + HttpOnly refresh 쿠키(회전) |
| POST `/auth/refresh` | refresh 쿠키(Origin 검증) | 회전. 재사용 감지 시 family 전체 폐기 |
| POST `/auth/logout` | refresh 쿠키 또는 access JWT | 204 멱등. 두 자격 모두 독립 폐기 |
| GET `/auth/me` | access JWT | 200 `{user}` |
| POST `/auth/evaluation-login` | 공개(Origin 검증, 활성 시에만 존재) | 본문 없음. 평가 계정의 일반 세션 발급, 별도 요청 한도 |
| GET `/auth/providers` | 공개 | `{googleEnabled, evaluationEnabled}` |
| GET `/auth/google/start` | 공개 | state·nonce·PKCE 생성, Google로 이동 |
| GET `/auth/google/callback` | state·브라우저 결합 검증 | 토큰 교환·계정 확인 후 고정 완료 화면으로303 |
| GET `/broadcasts` | 공개 | `{items,nextCursor}` cursor 페이지네이션(기본 20/최대 50). `status=live`(방송 중만), `ownerId=` 필터 |
| POST `/broadcasts` | 로그인 | 201. `ownerId`는 서버가 결정 |
| GET `/broadcasts/{id}` | 공개 | 404 포함 |
| PATCH `/broadcasts/{id}` | 소유자 | `version` 조건부 갱신, 충돌 409 |
| DELETE `/broadcasts/{id}?version=N` | 소유자 | soft delete, 204; 활성 회차가 있으면409 |
| POST `/broadcasts/{id}/sessions` | 소유자 | source, Idempotency-Key;202 준비 회차·단기 grant |
| GET `/broadcasts/{id}/sessions/current` | 공개 | 현재/최근 회차 또는 null; 민감 정보 제외 |
| POST `/broadcasts/{id}/sessions/{sid}/stop` | 소유자 |202 종료 처리, 이미 종료면200 |
| POST `/broadcasts/{id}/sessions/{sid}/publish-grants` | 원래 로그인 family의 소유자 | 조건을 만족하는 준비 회차의 만료 grant 갱신 |
| GET `/broadcasts/{id}/chat/stream` | 공개 | 라이브 채팅 SSE (최근 50개 + 실시간) |
| POST `/broadcasts/{id}/chat` | 로그인 | `{text}` 1~200자, 연속 전송 제한 |
| GET `/broadcasts/{id}/comments` | 공개 | 최근 댓글(오래된 순) |
| POST `/broadcasts/{id}/comments` | 로그인 | `{body}` 1~500자 |
| PATCH·DELETE `/comments/{id}` | 작성자 | 다른 사용자 403 |
| GET `/payments/config` | 공개 | `{enabled, mode:"test"}` |
| POST `/broadcasts/{id}/donations` | 로그인 | 주문 생성 `{amount, message}` → `{order}` |
| POST `/payments/test-checkout` | 주문한 후원자 | 테스트 결제창: `{orderId, approve}` → 결제 키(저장된 금액) |
| POST `/donations/confirm` | 주문한 후원자 | 결제사 승인·금액·대상 검증 후 확정, 반복 호출 시 중복 적립 없음 |
| POST `/payments/webhook` | 게이트웨이 | 게이트웨이 재조회 후 멱등 확정 |
| GET `/broadcasts/{id}/donations` | 공개 | 확정 내역·합계 |
| GET `/recordings`, `/recordings/{id}`, `/broadcasts/{id}/recordings` | 공개 | 다시보기 목록·상세 |
| GET `/recordings/{id}/media` | 공개 | MP4 (Range) |
| GET `/health/live`, `/health/ready` | 공개/내부 | 프로세스 생존 / DB·마이그레이션 준비 |

오류 형식: `{"error":{"code","message","requestId"}}`. 인증 세션의 최종 원장은 PostgreSQL `auth_session_families`/`auth_sessions`이며 Redis는 캐시일 뿐(장애 시 보호 API 503 fail-closed, 누락 시 원장으로 자가 복구).

## 보안 노트

- 비밀번호 Argon2id 저장, access JWT는 브라우저 메모리에만 보관(localStorage 미사용).
- refresh 토큰은 `Secure; HttpOnly; SameSite=Lax` 쿠키로만 전달하고 매 갱신마다 회전, 재사용 감지 시 전체 폐기.
- 인증 POST는 Origin 정확 일치 검증, 가입·로그인·refresh·평가 로그인에 요청 한도 적용.
- 송출(WHIP)은 소유자에게 발급한 단기 grant로만, 시청(HLS)은 서버 인가를 거쳐서만 허용합니다. DB·Redis·MediaMTX 관리 API는 외부에 노출하지 않습니다.
- 로그·응답에 비밀번호·토큰·DSN을 남기지 않습니다.

이 프로젝트는 AI 개발 도구(Codex, Claude Code 등)를 적극 활용해 설계·구현·검증했습니다.

## 라이선스/출처

- 백엔드: Go 표준 라이브러리, [pgx](https://github.com/jackc/pgx), [go-redis](https://github.com/redis/go-redis), [jwt](https://github.com/golang-jwt/jwt), [x/crypto](https://pkg.go.dev/golang.org/x/crypto) (Argon2id)
- 프런트엔드: Flutter, [http](https://pub.dev/packages/http), [provider](https://pub.dev/packages/provider)
- 미디어 서버: [MediaMTX](https://github.com/bluenviron/mediamtx) v1.21.3 (MIT) + 종료 근거 기록 패치(`experiments/media-completion/upstream.patch`), [FFmpeg](https://ffmpeg.org) (MediaMTX 공식 ffmpeg 이미지 포함), 역방향 프록시 [Caddy](https://caddyserver.com) (Apache-2.0)
- HLS 재생: [hls.js](https://github.com/video-dev/hls.js), 로컬 번들 `wbb-frontend/web/vendor/hls.min.js`와 라이선스·포함 구성요소 고지 `wbb-frontend/web/vendor/hls.js.LICENSE.txt`. 기존 미디어 실험에서 검증한 동일 번들을 사용하며 런타임 CDN에서 가져오지 않는다.
- 한글 폰트: [Noto Sans KR](https://github.com/google/fonts/tree/main/ofl/notosanskr) (SIL OFL 1.1, 라이선스 전문 `wbb-frontend/assets/fonts/OFL.txt`) — 폰트를 번들해 런타임 CDN 요청이 없다. 번들 폰트에 없는 글리프(예: 일부 이모지)는 로컬 폴백 경로(`/assets/fonts/fallback/`)로만 해석되며 미지원 글리프로 표시될 수 있다.


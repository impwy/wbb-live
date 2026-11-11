# WBB 구현 설계

작성: 2026-11-06. 상태: 구현 계약. **아래 기능은 아직 구현·실기기 검증 완료를 의미하지 않는다.** 사용자 요구사항은 `../requirements/01-contest-requirements.md`, 인수 검사는 `../requirements/02-acceptance-tests.md`, 실행 순서는 `../.scratch/wbb-live/spec.md`와 개별 티켓을 따른다. 초기 후보 문서보다 이 설계가 우선한다. 용어는 `../GLOSSARY.md`를 따른다.

## 1. 범위와 운영 목표

필수 범위는 이메일·비밀번호와 Google 로그인, 방송 정보 CRUD, PC 화면·공유 오디오·마이크 송출, 모바일 카메라·마이크 송출, 비로그인 시청, 서버 권한 검증, 실제 송출 종료, 영속 저장, 평가 가능한 공개 서비스와 README다. Flutter는 Web으로 배포하며 네이티브 앱은 만들지 않는다. 댓글·채팅·도네이션·녹화·다시보기는 이번 명세에서 제외한다.

서비스는 `https://live.wombatburrow.com`을 사용한다. CDN 없이 가정의 Labtop 서버와 Wi-Fi 회선에서 직접 제공한다. 기존 도메인과 서비스는 유지한다. 동시 방송 10개·시청자 500명은 부하 검증 목표이며 지원 보장이 아니다. 배포 전 측정한 업로드·CPU·GPU·메모리·디스크 여유에 따라 실제 입장 제한을 설정한다. 예를 들어 시청자 500명이 평균 3Mbps를 받으면 영상만 약 1.5Gbps가 필요하다. 목표 달성 여부는 실측으로 보고한다.

## 2. 구성과 책임

```text
Flutter Web ─ HTTPS ─ Caddy ─ 정적 웹
                    ├ /api/v1 ─ Go API ─ PostgreSQL
                    │                   ├ Redis
                    │                   └ transactional outbox
                    └ /media ─ 접근 검사 ─ MediaMTX LL-HLS
브라우저 ─ WHIP signaling(API 중계) ─ MediaMTX
브라우저 ═ WebRTC 미디어(UDP/TCP 또는 TURN) ═ MediaMTX ingest
MediaMTX ingest ─ FFmpeg VAAPI ─ MediaMTX rendition paths
outbox ─ Kafka ─ Go worker(멱등 소비/복구)
API/worker/media/infra ─ OTel·Alloy ─ Loki·Tempo·Mimir ─ Grafana
```

- Go 모듈형 모놀리스: 인증, 방송, 미디어 제어, 공개 조회, 운영 상태를 분리한다. API와 worker는 같은 도메인 계약을 공유하되 독립 프로세스로 실행한다.
- PostgreSQL은 사용자·방송·회차·인증 세션·이벤트의 원장이다. Redis는 제한 카운터와 단기 캐시에만 사용하고 권한·회차 상태의 유일한 원장으로 쓰지 않는다. Kafka는 비동기 이벤트 처리에 사용하며 시작·종료의 즉시 권한 차단을 Kafka 수신까지 미루지 않는다.
- MediaMTX는 WHIP 입력과 LL-HLS 출력을 담당한다. FFmpeg가 디코딩·1080/720/480 변환·오디오 AAC 변환을 담당한다. 각 회차에 FFmpeg 감독 작업 하나를 배정하며 중복 실행을 DB lease로 막는다.
- Caddy는 TLS, 경로 라우팅, 정적 웹과 미디어 전달을 담당한다. 영상 바이트를 일반 Go API 응답으로 중계하지 않는다. WHIP의 SDP/ICE 제어 메시지만 Go를 거친다.
- 모든 데이터 인스턴스와 LGTM은 WBB 전용이다. 기존 공유 서비스의 데이터 삭제·중단·재구성으로 자원을 확보하지 않는다.

## 3. 데이터와 불변조건

모든 ID는 서버 생성 UUID, 시간은 UTC `timestamptz`, 외부 JSON 시간은 RFC3339다. SQL 마이그레이션을 버전 관리하고 애플리케이션 시작 시 자동 파괴적 마이그레이션을 하지 않는다.

| 테이블 | 주요 정보 / 제약 |
| --- | --- |
| users | id, 정규화 이메일, 표시 이름, nullable password_hash, 생성 시각. 로컬 이메일 중복 거절 |
| auth_identities | user_id, provider, issuer, subject. `(provider, issuer, subject)` 유일 |
| auth_session_families | id, user_id, revoked_at. 회전·로그아웃·재사용 탐지가 같은 행 잠금을 사용하며 계열 전체의 폐기를 판정 |
| auth_sessions | id, user_id, refresh_token_hash, refresh_family_id, expires_at, revoked_at, rotation 정보. 원문 토큰 저장 금지 |
| broadcasts | id, owner_id, title(1~100자), description(0~2000자), version, created_at, updated_at, deleted_at |
| broadcast_sessions | id, broadcast_id, publisher_auth_family_id, initial_auth_session_id(감사용), state, version, media_generation, 시작/종료/heartbeat 시각, end_reason, ingest_connection_id, worker_lease_until |
| media_create_operations | id, broadcast_session_id, media_generation, grant_jti, state(pending/attached/cleanup_pending/closed), upstream_connection_id, created_at, resolved_at |
| outbox_events | event_id, aggregate_id, aggregate_version, type, occurred_at, payload, traceparent, published_at, attempts |
| consumer_inbox | consumer_name, event_id, processed_at. 두 컬럼 복합 유일 |

DB 부분 유일 인덱스로 방송별 `STARTING/LIVE/STOPPING` 회차를 최대 하나로 제한한다. 방송 메타데이터와 송출 회차는 분리한다. 다시 시작하면 새 회차 ID와 미디어 경로를 사용한다. 과거 회차의 콜백·재시도·캐시가 새 회차에 영향을 주지 않는다. 제목·설명은 일반 텍스트이며 HTML로 렌더링하지 않는다.

`broadcasts.version`을 수정·삭제 요청에 포함하고 조건부 갱신한다. 오래된 버전은 409다. 현재 활성 회차가 있으면 삭제는 409다. 삭제는 soft delete이며 공개 조회에서 제외한다. 내부 감사·종료 처리를 위해 회차 레코드를 유지한다.

## 4. 로그인과 권한

- 이메일 가입에는 검증된 Argon2id 구현을 사용한다. 길이 제한과 비용은 벤치마크 후 설정하고 비밀번호를 로그에 남기지 않는다. 비밀번호 12~128자, 이메일 최대254자, 표시 이름1~40자로 제한한다. 비밀번호를 잘라 저장하지 않는다.
- 자체 access JWT는 10분, 회전 refresh token은 최대7일을 초기 설정으로 한다. JWT는 고정 허용 알고리즘·issuer·audience(`wbb-api`)·exp·sub·sid를 검사한다. JWT의 권한 정보만 믿지 않고 모든 인증 API에서 활성 `auth_sessions`를 확인한다. 따라서 로그아웃 후 기존 access JWT도 거절한다. 공개 목록/시청에는 이 검사가 필요 없다.
- access JWT는 브라우저 메모리에만 저장한다. refresh는 동일 출처 `Secure; HttpOnly; SameSite=Lax; Path=/api/v1/auth` 쿠키로 전달한다. localStorage에 토큰을 저장하지 않는다. register/login/refresh/logout은 POST와 정확한 Origin 검증을 적용한다. cross-origin API 사용은 기본 거절한다.
- refresh는 DB 트랜잭션으로 회전한다. 회전·로그아웃·재사용 탐지는 같은 family 행 잠금으로 직렬화한다. 소비한 토큰 재사용은 동시 요청으로 발견된 경우에도 해당 family를 폐기하고 커밋한다. 보호 API는 family와 개별 session의 폐기를 모두 확인한다. Flutter는 refresh single-flight를 사용한다. 다중 탭 간에도 갱신을 직렬화하거나 세션 재로그인을 안내하며 이전 토큰을 무제한 허용하는 방식으로 경쟁을 숨기지 않는다.
- Google은 Authorization Code + PKCE, state, nonce, 허용된 redirect URI와 서버 측 토큰 교환을 사용한다. issuer/audience/expiry/nonce 및 provider subject를 검증한다. ID token은 Google JWKS 검증 라이브러리를 사용한다. 이메일이 같다는 이유만으로 기존 로컬 계정에 자동 연결하지 않는다. 충돌 시 기존 로그인 방법을 안내하며 계정 연결 기능은 후속 범위다.
- OAuth callback은 access token을 URL로 내보내지 않는다. 서버가 refresh 쿠키를 설정하고 고정된 프런트엔드 경로로 이동한 뒤 refresh API로 access token을 받는다. 인증 code/state/토큰이 Caddy·API 로그에 남지 않게 해당 경로의 query를 제거한다.
- 서버가 JWT subject에서 방송 owner를 결정한다. 요청의 ownerId는 받지 않는다. 수정·삭제·시작·종료·송출 인증마다 DB owner를 검사한다. anonymous401, 다른 사용자403, 없는/삭제된 방송404, 충돌409다.
- 로그인·가입·refresh에는 IP 및 계정 단위 제한을 적용한다. Redis 장애 시 인증이 필요한 보호 API는503으로 거절(fail closed)하고 익명 목록·시청은 계속 제공한다. Redis의 active sid만으로 허용하지 않고 PostgreSQL auth_sessions의 revoked_at·expires_at을 항상 최종 판정한다. 캐시 삭제 실패나 늦은 재적재로 폐기된 인증이 부활할 수 없어야 한다. 실제 사용자 IP는 신뢰하는 프록시에서만 전달받는다.
- 송출의 로그인 식별자는 refresh 회전으로 바뀌는 개별 sid가 아니라 `auth_session_family_id`다. 현재 유효한 sid의 family를 서버에서 확인한다. 최초 sid는 감사용이며 정상 refresh가 방송을 끊지 않는다. resource의 PATCH/DELETE는 동일 family를 요구한다. 별도 로그인한 동일 owner도 방송 stop API로 자신의 회차를 종료할 수 있다.
- 로그아웃·refresh 재사용 폐기는 해당 family가 시작한 활성 회차를 STOPPING으로 전환하고 family를 폐기하는 DB 트랜잭션이다. 미디어 종료 실패와 무관하게 인증은 폐기하고 종료 작업을 재시도한다. UI도 모든 로컬 트랙을 닫는다.

## 5. HTTP 계약

외부 prefix는 `/api/v1`. 외부 JSON 키는 camelCase다. DB와 내부 이벤트의 snake_case와 구분한다. 오류 형식은 `{error: {code, message, requestId}}`; 내부 오류·SQL·토큰을 포함하지 않는다. 요청 body 최대64KiB, WHIP SDP는 별도 제한을 둔다. 목록 기본20개/최대50개, 불투명 cursor를 사용한다. 목록은 변경되지 않는 createdAt,id 내림차순 cursor를 사용한다. 페이지 중 방송 시작·종료로 정렬이 바뀌지 않게 한다.

| 메서드 / 경로 | 입력·권한 | 성공 응답 |
| --- | --- | --- |
| GET /health/live | 공개, 상세 시스템 정보 없음 | 200 process alive |
| GET /health/ready | 내부 운영 경로 | DB 연결·마이그레이션 준비 시200, 아니면503 |
| POST /auth/register | email,password,displayName | 201 {user}; 자동 로그인하지 않음 |
| POST /auth/login | email,password | 200 {accessToken,user}, refresh 쿠키 |
| POST /auth/refresh | refresh 쿠키, Origin 검증 | 200 {accessToken,user}, 회전 쿠키 |
| POST /auth/logout | refresh 쿠키 또는 access JWT, Origin 검증 | 204 쿠키 삭제; 반복 요청도204 |
| GET /auth/me | access JWT | 200 {user:{id,displayName,email}} |
| GET /auth/google/start | 고정 callback만 허용 | 302 Google 인증 |
| GET /auth/google/callback | code,state 검증 | 303 고정 웹 경로 |
| GET /broadcasts | 공개, cursor,limit; status=live/all | 200 {items,nextCursor} |
| POST /broadcasts | 로그인, title,description | 201 {broadcast: 방송 객체} |
| GET /broadcasts/{id} | 공개 | 200 {broadcast: 방송 객체} |
| PATCH /broadcasts/{id} | 소유자, version,title/description | 200 {broadcast: 갱신 객체} |
| DELETE /broadcasts/{id} | 소유자, query version | 204; 활성 회차 있으면409 |
| POST /broadcasts/{id}/sessions | 소유자, source=screen/camera, Idempotency-Key | 202 회차 객체 및 publish grant |
| GET /broadcasts/{id}/sessions/current | 공개, 민감 정보 제외 | 200 현재/최근 회차 또는 null |
| POST /broadcasts/{id}/sessions/{sid}/stop | 소유자 | 202 STOPPING, 이미 ENDED면200 |
| POST /broadcasts/{id}/sessions/{sid}/publish-grants | 소유자, STARTING에서만 | 201 단기 grant |
| POST /live-sessions/{sid}/whip | publish grant | WHIP 규격 응답, 외부 안전 Location |
| PATCH /live-sessions/{sid}/whip/{resource} | access JWT·resource 소유권·활성 회차 | WHIP 규격 ICE 처리 |
| DELETE /live-sessions/{sid}/whip/{resource} | access JWT·resource 소유권 | 202 종료 요청 수락; 실제 완료는 회차 ENDED로 확인 |
| GET /broadcasts/{id}/playback | 공개 | LIVE면200 {sessionId,masterUrl}; 미송출409 |
| POST /playback-events | 공개, 크기/빈도/필드 제한 | 202 집계용 이벤트 수락 |

방송 객체는 `id, ownerId, owner:{id,displayName}, title, description, version, status, currentSessionId, createdAt, updatedAt`만 공개한다. `status`는 활성 회차의 상태, 회차가 없으면 READY, 마지막 회차가 종료됐으면 ENDED다. 외부 JSON 상태값은 `ready`, `starting`, `live`, `stopping`, `ended`로 소문자 직렬화한다. 도메인 상태표의 대문자와 의미는 같다. 이메일·로그인 세션·grant·내부 미디어 주소는 포함하지 않는다. 회차 생성 응답은 {session:{id,broadcastId,state,version,startedAt,endedAt},publishGrant:{token,expiresAt,whipUrl}}이다. 이 생성 응답과 grant는 소유자에게만 보내고 캐시하지 않는다. 공개 현재/최근 회차 조회는 {session: null 또는 {id,broadcastId,state,version,startedAt,endedAt}}만 반환하며 family·grant·미디어 내부 식별자를 포함하지 않는다.

Idempotency-Key는 로그인 사용자·방송·작업별로 묶어24시간 저장한다. 같은 키/같은 입력은 동일 회차로 응답하고, 같은 키/다른 입력은409다. 다른 키로 활성 회차를 중복 시작해도409다. 이미 송출 중인 회차에 새 publish grant를 발급해 두 번째 송출자가 기존 연결을 탈취하게 하지 않는다. grant가 만료된 STARTING 회차만 갱신할 수 있다.

시작 재응답은 새 회차나 새 grant를 만들지 않는다. 원래 로그인 family의 요청이며 회차가 STARTING이고 원래 jti가 미폐기·미소비·미만료일 때만 동일 자격을 반환한다. 다른 family의 같은 키 요청은403이다. STOPPING/ENDED 또는 폐기된 jti는 `publishGrant:null,publishGrantState:"unavailable"`을 반환한다. 소비·만료·생성 중이면 `publishGrant:null`과 `publishGrantState`(`consumed`/`expired`/`pending`)를 반환한다. grant 원문을 로그에 남기지 않는다. 갱신 endpoint는 같은 로그인 family, STARTING, 준비 제한 이전, 이전 grant 만료, 미해결 생성 작업 없음 조건을 모두 검사한다. 이전 jti를 폐기하고 새 jti를 원자적으로 발급한다.

## 6. 송출 회차 상태와 종료

| 현재 상태 | 사건 | 다음 상태 / 처리 |
| --- | --- | --- |
| 없음/ENDED | 소유자 시작, 입장 제한 통과 | 새 STARTING 회차 생성 |
| STARTING | ingress 연결 + 미디어 출력 준비 확인 | LIVE |
| STARTING | 60초 준비 제한, 사용자 취소, 장치 오류 | STOPPING |
| LIVE | 종료 요청, 공유 트랙 종료, 로그인 세션 폐기 | STOPPING |
| STARTING/LIVE | 최초 ingress 관찰 이후 유실 연속15초 | STOPPING |
| STOPPING | 미해결 생성 작업 없음 + ingress 없음 + 변환 프로세스 없음 확인 | ENDED |
| ENDED | 모든 늦은 ready 이벤트 | 무시; 재시작은 새 회차 |

위60초/15초는 초기 설정값이며 실측으로 조정한다. 상태 전이는 조건부 UPDATE와 회차 version으로 보호한다. LIVE 승인은 단순 HTTP 시작 성공이 아니라 실제 ingest와 지원 rendition의 유효한 manifest/segment를 관찰한 경우다. 상태·outbox 기록은 같은 DB 트랜잭션이다.

준비 제한은 회차 생성 시각부터60초이며 grant 갱신으로 연장하지 않는다. 최초 ingress 연결 전에는15초 유실 제한을 적용하지 않는다. 초기 grant TTL은30초(상한120초), 실제 만료는 준비 제한을 넘지 않는다. 준비 제한 경과 후에는 이전 회차를 정리하고 새 회차로 재시도한다.

종료 절차는 다음 순서로 실행한다.

1. DB에서 STOPPING으로 바꾸고 새 publish/시청 manifest 접근을 차단한다.
2. 미디어 제어 어댑터가 MediaMTX의 해당 ingress 연결을 명시적으로 끊는다. JWT 만료에 의존하지 않는다.
3. 해당 회차 FFmpeg를 TERM 후 제한시간 경과 시 KILL하고 하위 프로세스까지 회수한다. 다른 회차 PID를 건드리지 않는다.
4. 실제 연결·프로세스 부재를 확인한 후 ENDED와 end_reason/ended_at을 기록한다. 외부 제어 실패는 STOPPING에 남고 지수 backoff와 최대 대기 간격30초로 계속 재시도한다.

이미 수신된 버퍼 영상은 즉시 회수할 수 없다. 종료 후 새 manifest/segment 다운로드는 거절하고 플레이어 상태 polling(초기2초)으로 버퍼를 중단한다. 종료 완료 후 재발행·재시청용 이전 주소 재사용이 새 연결을 만들지 못하는지 검사한다. 브라우저 탭 종료 메시지는 유실될 수 있어 서버 재조정이 필수다.

API/worker 시작 시 및 주기5초마다 활성 회차와 실제 미디어 상태를 대조한다. worker lease는 단일 소유권을 보장하되 시간이 지난 lease를 인계할 수 있어야 한다. 콜백은 private network 인증을 거치고 `session_id, media_generation, connection_id`와 현재 상태를 검사한다. 중복·역순 이벤트는 무해해야 한다. DB 재시작 후 연결이 남은 경우에도 검증 없이 ENDED→LIVE로 바꾸지 않는다.

WHIP upstream 생성 전에 회차·generation·grant jti에 연결된 생성 작업을 DB에 예약한다. 회차별 미해결 생성 작업은 하나만 허용하고 동일 grant의 동시 POST는409로 거절한다. 송출할 SDP digest와 검증된 MediaMTX contract/root/slot/epoch/sequence를 작업에 고정한 뒤 첫 upstream byte를 보낸다. STOPPING 이후 도착한 생성 성공은 클라이언트에 공개하지 않는다. 응답 유실·프로세스 재시작으로 Location을 몰라도 저장된 operation ID와 epoch로 close/query를 재시도한다. upstream 4xx·DELETE 200/204/404·자원 부재는 완료 증거가 아니다. 해당 작업의 검증된 `completed` 또는 직렬화된 never-sent 증거, FFmpeg 실행 종료 및 새 자원 snapshot을 확인한 뒤에만 ENDED를 확정한다. 계약이 없는 구형 미완료 작업은 새 epoch를 소급 대입하지 않고 STOPPING에 남긴다. 운영 처리 기준은 [구형 미디어 작업 절차](operations/legacy-media-operations.md)를 따른다.

## 7. WHIP 인증과 브라우저 캡처

publish grant는 로그인 JWT와 별개이며 `aud=wbb-publish`, 고유 jti, 회차·방송자·로그인 family·generation에 묶인 단기 일회성 자격이다. TTL은 §6을 따른다. URL query에 넣지 않고 Authorization 헤더로 보낸다. API 중계 POST는 grant 서명·서버 family 활성 상태·owner·회차 상태를 확인하고 생성 작업을 예약한다. family 활성은 DB에서 family가 미폐기이며 그 family에 현재 비폐기·미만료 auth session이 존재하는 것으로 판정한다. 최초 sid의 만료로 정상 회전 후 권한을 끊지 않는다. 생성한 resource는 서버에 owner·family·회차·generation으로 매핑한다. 이후 PATCH/DELETE는 갱신 가능한 access JWT와 해당 매핑으로 인증하므로 grant 만료가 장시간 방송의 ICE 처리·정리를 막지 않는다. 로그아웃 후에는 서버 종료 작업이 resource를 정리한다. STOPPING/ENDED면 새 POST/PATCH를 거절하고 정리용 DELETE만 허용한다. 중계하는 Location은 동일 출처의 불투명 resource ID로 다시 쓰며 임의 upstream URL을 입력받지 않는다.

MediaMTX ingress endpoint는 외부 직접 노출하지 않는다. 외부 인증 콜백을 통해 회차의 활성 여부를 재검사하고 두 번째 publisher가 첫 publisher를 교체하지 못하게 설정한다. FFmpeg의 내부 read/publish 자격과 브라우저 grant는 분리하고 최종 rendition 경로에 브라우저가 직접 publish하지 못하게 한다. 버전별 WHIP Location/PATCH/DELETE와 MediaMTX 연결 종료 API 지원은 02번 검증에서 확인하고 어댑터에 가둔다. 외부 HTTP 인증의 action/path 검사는 [MediaMTX 인증 문서](https://mediamtx.org/docs/features/authentication)를 기준으로 한다(2026-11-06 확인).

PC는 사용자 클릭 직후 `getDisplayMedia`를 호출한다. 선택한 화면/탭 영상과 공유 오디오를 받고 `getUserMedia`로 마이크를 받는다. Web Audio로 공유 소리와 마이크를 한 audio track으로 합성한다. muted 로컬 미리보기, clipping을 피하는 gain, 장치 상태 안내를 제공한다. 공유 소리가 없는 경우 이를 표시하고 오디오 포함 탭 재선택을 안내하며 필수 PC 인수 검사를 통과했다고 처리하지 않는다. macOS 전체 시스템 소리 등 검증하지 않은 조합을 지원한다고 표기하지 않는다. 사용자 활성화와 화면·오디오 선택 제약은 [MDN getDisplayMedia](https://developer.mozilla.org/en-US/docs/Web/API/MediaDevices/getDisplayMedia)를 따른다(2026-11-06 확인).

모바일은 Flutter Web에서 카메라·마이크를 받고 `playsinline` 미리보기를 쓴다. 전면/후면 선택은 현재 회차를 안전하게 유지할 수 있을 때만 제공한다. 백그라운드·화면 잠금 송출 지속을 보장하지 않는다. 권한 거부·공유 취소·부분 획득 실패 시 모든 track, AudioContext, PeerConnection을 정리한다. 화면 공유 track의 ended는 사용자 종료와 같은 경로로 처리한다.

WebRTC 미디어는 Caddy HTTP 경로와 별도다. UDP ICE 포트, 필요 시 TCP/TURN relay, NAT 후보와 실제 외부망 연결을 검증해야 한다. STUN만으로 모든 이동통신망 접속을 보장하지 않는다. TURN 필요 여부를 실측하며 필요하면 자체 TURN을 범위에 포함한다. 공인 IP·포트·자격은 비공개 환경 설정으로 관리한다.

## 8. LL-HLS와 GPU 변환 관문

목표 출력은 H.264/AAC, 최대30fps,1080p·720p·480p다. 소스보다 큰 해상도로 확대하지 않으며 실시간 처리가 가능한 rendition만 노출한다. 초기 목표 비트레이트는 각각4.5/2.5/1Mbps, 오디오128kbps이고 실제 화질·업로드 검사로 조정한다. 소스의 실제 코덱/해상도/프레임률/오디오 트랙을 확인한 뒤 FFmpeg 그래프를 만든다. 브라우저 입력 코덱에 따라 소프트웨어 디코딩이 필요할 수 있으며 VAAPI 인코딩 성공만으로 하드웨어 디코딩을 가정하지 않는다.

같은 입력 시계에서 각 rendition을 분기하고 GOP/IDR 시각·프레임률을 맞춘다. 초기 segment2초/part200ms를 검증 출발점으로 삼되 MediaMTX 버전이 실제 제공하는 manifest와 일치시킨다. MediaMTX HLS는 한 입력의 첫 video track만 처리하므로 여러 비디오 트랙을 같은 경로에 넣어 ABR을 구현하지 않는다. rendition별 경로 세 개를 나열하는 것만으로 ABR이 완성되었다고 간주하지 않는다. 먼저 단일 rendition으로 실제 송출·시청을 검증하고, 그 다음 ABR 관문을 통과한다.

**03번 미디어 검증이 필수 차단 관문이다.** 다음을 증명해야 본 구현이 해당 구성을 전제로 진행한다.

- 독립 MediaMTX rendition 경로의 media sequence, timestamp, discontinuity와 keyframe이 서로 정렬되는가.
- master의 BANDWIDTH/AVERAGE-BANDWIDTH/RESOLUTION/CODECS가 실제 출력과 맞는가. 상대 경로는 외부 `/media` 아래에서 안전하게 해석되는가.
- native Safari 및 hls.js가 LL-HLS blocking reload/parts를 처리하고 재생 중1080↔720↔480 전환 시 시간 이동·장시간 정지·오디오 손실이 없는가.
- rendition readiness/실패를 master 갱신에 반영하며, 최신 목록에 없는 품질을 플레이어가 계속 요청하지 않도록 복구하는가.
- MediaMTX 단독 복수 경로로 안정된 ABR이 불가능하면 결과와 필요한 packager 변경을 Codex에 보고한다. 검증 없이 FFmpeg 기본 HLS 출력을 LL-HLS라고 부르거나 멀티 비트레이트 완료로 표시하지 않는다. 한 품질 검증은 필수 라이브 기능의 중간 성과로 기록할 수 있으나 ABR 인수 완료가 아니다.

제품 플레이어는 실제 검증한 브라우저별 재생 경로를 Flutter HTML view에 붙인다. 로컬 실험의 Chrome은 native HLS 지원 응답과 달리 실제 재생이 실패해 hls.js/MSE를 우선 사용한다. Safari native HLS 정책은 실제 기기 검증 후 확정하며, 지원 여부 문자열만으로 재생 가능하다고 판정하지 않는다. 이 실험 결과가 제품 Flutter 플레이어 구현 완료를 뜻하지 않는다. 자동재생 거절 시 재생/소리 켜기 버튼을 제공한다. quality=auto와 검증된 고정 품질을 제공하고 시청 화면은 비로그인으로 열린다. manifest/segment는 public URL이어도 session 상태 gate를 거친다. 캐시로 종료 후 gate를 우회하지 않도록 no-store를 기본으로 하고 CDN은 사용하지 않는다. Caddy 프록시는 LL-HLS 요청의 query를 유지하며 blocking reload를 임의 버퍼링·짧은 timeout으로 깨뜨리지 않는다.

## 9. Kafka·복구·영속성

topic `wbb.broadcast.events.v1`, key는 broadcast_id. envelope는 `event_id,type,schema_version,aggregate_id,aggregate_version,occurred_at,payload,traceparent`. 사용자 이메일·grant·비밀번호는 payload에 넣지 않는다. outbox 발행 후 마킹 사이 장애로 중복될 수 있으므로 정확히 한 번 전달을 약속하지 않는다. consumer의 상태 변경과 inbox 저장은 동일 DB 트랜잭션으로 수행한다. 순서가 뒤집힌 aggregate_version은 현재 상태와 비교해 적용하지 않는다.

Kafka 장애 시 상태 전이·종료는 DB와 직접 미디어 제어로 진행하고 outbox에 쌓는다. 발행 재시도, oldest event age, poison event 격리와 재처리 절차를 제공한다. 타이머/lease/reconciler가 Kafka 유실·지연과 무관하게 종료 작업을 발견한다. Redis 장애 시 보호 API는503으로 거절하고 익명 조회·시청은 계속한다. 정상 상태에도 PostgreSQL 원장의 인증 폐기 여부를 최종 확인하며 캐시만으로 허용하지 않는다. 서버 내부 종료·복구 작업은 Redis 장애와 무관하게 PostgreSQL과 미디어 제어로 진행한다.

PostgreSQL 영속 볼륨과 백업·복원 검사를 제공한다. 재시작 테스트는 컨테이너 재생성에도 볼륨을 유지하여 방송 메타데이터와 소유자가 보존되는지 확인한다. 개발용 volume 삭제 명령을 일반 실행 방법에 섞지 않는다.

## 10. 관측성

OpenTelemetry와 LGTM은 대체 관계가 아니다. Go가 OTel metrics/traces를 만들고 JSON slog를 stdout으로 한 번 기록한다. Alloy가 수집하여 Loki(logs), Tempo(traces), Mimir(metrics)로 보내고 Grafana로 조회한다. WBB 전용 인스턴스·볼륨을 사용한다. trace_id를 로그에 연결하고 API→DB→outbox→Kafka→worker에는 trace context와 비동기 span link를 전달한다.

- API: 요청 수·오류율·지연 p95, 인증 실패율, DB pool 대기.
- 방송: 시작 성공/실패·준비 시간, 활성 회차, 종료 재시도·STOPPING 체류 시간.
- 미디어: FFmpeg speed/fps/종료 코드, rendition 수, manifest 최신성, player 최초 재생/오류/rebuffer/품질.
- 인프라: CPU·RAM·디스크·네트워크·pod restart, Kafka lag, outbox 적체·최고 연령.

네 개 대시보드(API, 방송/시청, 미디어, 인프라/이벤트)를 제공한다. metric label은 service/environment/route template/status/resolution 같은 유한 집합이다. user_id/broadcast_id/전체 URL/query/token은 label로 쓰지 않는다. 민감한 header/body/OAuth code는 telemetry에서 제외한다.

개발 trace100%, 운영은 parent-based10%를 초기값으로 한다. 오류 로그·오류 metric은 별도로 수집하며 모든 오류 trace가 남는다고 보장하지 않는다. logs/traces3일, metrics7일의 초기 retention과 저장 상한을 둔다. 큐·batch·retry는 bounded/nonblocking으로 하고 exporter 장애는 송출·로그인을 막지 않으며 drop count를 관측한다. 브라우저는 공개 OTLP에 직접 쓰지 않고 검증·빈도 제한된 playback-events API로 보낸다.

Grafana와 OTLP는 관리망에서만 접근한다. 외부 Slack/이메일 알림은 이번 범위에 없다. 초기 알림은 Grafana 안에서 디스크 부족, API 오류, 반복 종료 실패, outbox 적체를 확인한다. 개인 식별 데이터를 수집하지 않는 최소 이벤트 집계를 우선한다.

## 11. 배포·성능·보안 관문

기존 k3s 환경 안에 WBB 전용 namespace, service account, Secret, PVC를 둔다. Caddy가 유일한 공개 HTTP 진입점이며 DB/Redis/Kafka/MediaMTX 관리 API/OTLP/Grafana는 공개하지 않는다. `/dev/dri/renderD128` 접근과 실제 컨테이너 내 VAAPI 동작을 확인한다. image/version은 검증한 버전과 digest로 고정하고 latest에 의존하지 않는다. secret 예시는 자리표시자만 저장한다.

DNS는 `live` 하위 도메인의 DNS only로 구성한다. CNAME은 대상 체인 전체가 프록시되지 않는 경우에만 목적에 맞는다. 기존 루트 도메인의 레코드·Caddy host·인증서를 변경하지 않는다. 미디어 포트/NAT/TURN은 별도 검증하고 Cloudflare IP만 허용하는 기존 방화벽 정책이 있다면 WBB host의 직접 접근에 필요한 최소 변경만 설계한다.

현재 서버의 GPU 단기 변환 성공은 장기 안정성이나 동시10개 성능을 증명하지 않는다. 현재 제한된 RAM/디스크, Wi-Fi 업로드 실측 부재를 운영 위험으로 관리한다. 다음 순서로 증거를 쌓는다: 회차1개30분 →2개 →4개 → 측정 여유가 있을 때만10개. 각 단계에서 실시간 speed≥1, 프레임 드롭, 열 제한, 오류, 메모리, 디스크, 업로드와 다른 서비스 영향을 기록한다. 시청 부하는 실제 인터넷 경로와 로컬 경로를 분리해 결과를 보고한다.

운영 입장 제한은 `MAX_ACTIVE_BROADCASTS`와 측정한 미디어 자원으로 설정한다. 최초 운영은1개에서 시작하고 측정 후 올린다. 거절은503 `capacity_exceeded`와 사용자 안내를 제공한다. 시청자 수 추정은 HLS 요청 수와 동일하지 않으므로 추정임을 표기한다. 평가 README에 검증된 지원 규모만 적는다.

## 12. 인수와 완료 보고

자동 검사: Go format/vet/test/race(지원 환경), Flutter analyze/test/web build, 실제 PostgreSQL API 통합 검사, 직접 HTTP 권한·중복·상태 검사, dependency/secret 검사. 코드를 따라 쓰는 테스트보다 사용자에게 보이는 HTTP와 UI 결과를 검증한다. 새 프로젝트라 기존 테스트 선례는 없으며 각 티켓에서 실행 가능한 최소 경계를 만든다.

실제 기기 검사: PC Chrome/Edge 탭 소리+마이크를 별도 기기에서 함께 듣기, Android Chrome/iOS Safari 카메라+마이크를 별도 기기에서 보기/듣기, 외부 이동통신망 접속, 공유 중지/탭 닫기/로그아웃/재시작, 이전 grant 거절, 품질 전환. 브라우저/OS/기기/날짜/네트워크/결과를 기록한다. 보유하지 않은 기기는 미검증으로 남기고 지원표에 반영한다. 모의 track·unit test·localhost 성공을 실제 소리/기기/인터넷 성공과 합치지 않는다.

README에는 서비스 URL, 실제 동작하는 평가 로그인 방법, 런타임·환경변수 예시, 실행·migration·중지·백업 방법, PC/모바일 송출·시청·종료 절차, 검증된 지원 환경, 알려진 제약, OSS 출처를 포함한다. 평가용 계정은 운영자가 별도로 주입하고 개인 자격이나 비밀번호를 저장소에 하드코딩하지 않는다. 미완료·실행 불가 항목은 명시한다.

설계 변경은 근거·영향을 ADR로 남긴다. 특히 ABR packager 변경, TURN 추가, 용량 목표 미달, OAuth 외부 설정 미완료는 숨기지 않고 완료 보고에 포함한다.

## 로컬 구현 순서 예외 (2026-11-09)

사용자는02 실제 휴대폰·외부망 인수가 보류된 상태에서04 로컬 구현부터 진행하도록 승인했다.01 인증·CRUD와 검증된 단일 품질 실험을 바탕으로 회차 제어→인증 WHIP/변환→Flutter 송출·익명 시청 순서로 구현한다. 실제 기기·외부망·VAAPI·ABR와 최종 인수 기준은 유지하며 로컬 자동 검사로 대체하지 않는다.

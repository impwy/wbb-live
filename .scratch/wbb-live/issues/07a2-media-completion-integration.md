# 07-A2: 종료 완료 계약의 제품 통합

**Status:** in-review (로컬 제품 흐름 및 자연 종료·재시도 검증 완료; 전체 제품 장애 매트릭스와 공개 인수는 별도)
**Blocked by:** 07-A1 실제 Linux 미디어·장애 매트릭스, 독립 검증 및 ADR0001 채택(2026-11-11 충족). 구현자는 별도 동결·인계 후 단독으로 시작한다.
**목표:** known/unknown 생성 작업 모두 정확한 완료 근거를 저장한 뒤에만 closed 및 방송 ENDED를 확정한다.

## 현재 코드에서 교체할 판단

- `httpapi/media_handlers.go:handleWHIPPost`의 upstream 4xx → `livesessions.CloseFailedPublish` 즉시 closed.
- `handleWHIPResource`의 DELETE 200/204/404 → `CloseResource` 즉시 closed. 삭제 요청 접수·목록 부재와 실제 작업 완료를 구분한다.
- `mediaworker/worker.go:cleanup`의 known Location 삭제 및 5초 부재 → closed. 부재 시간은 미래 생성 불가능의 근거가 아니다.
- `livesessions.MarkDispatched`는 operation 행을 갱신하면서 JOIN된 session 상태를 읽지만 Stop과 동일 session 행 잠금을 취하지 않는다. 세션 상태 확인, 실행 귀속 및 dispatch 기록을 기존 잠금 순서와 직렬화해야 한다. 이미 dispatch된 요청이 stop 뒤 도착하는 경우는 upstream close/register 계약으로 봉쇄한다.

## 통합 계약 초안

1. 검증된 관리 lifecycle의 contract version, root, slot, 무작위 epoch와 sequence를 확인한다. 정확한 operation ID·session·generation·source path·SDP digest와 실행 귀속을 DB에 commit한 뒤 첫 upstream byte를 전송한다. 트랜잭션 안에 네트워크 대기를 넣지 않는다. 귀속을 이후 관측한 epoch로 덮어쓰지 않는다.
2. 미디어 client에서 publish/close/query 서명과 응답 검증을 집중 관리한다. 외부 사용자가 보낸 내부 헤더는 신뢰하지 않는다. 반환 tuple·contract·operation·상태가 요청과 일치해야 한다. timeout, 임의 4xx, 알 수 없는 epoch, 포화 미등록 상태, 계약 불일치는 종료 완료가 아니다.
3. STOPPING 및 기존 송출·시청 권한 폐기를 먼저 commit한다. 알려진 Location 유무와 무관하게 저장한 tuple로 close/query한다. HTTP 응답 유실·API/worker 재시작 후에도 동일 귀속으로 재시도한다. close 요청 수락과 완료를 별도 취급한다.
4. 정확한 operation completed 또는 검증된 과거 epoch 종료를 영속 기록한다. 그 뒤 기존 fencing·프로세스 신원 검사에 따라 FFmpeg 종료를 확인하고 최신 미디어 자원 조회를 수행한다. 모든 작업의 완료와 실제 자원 정리를 확인한 경우에만 기존 상태 전이/outbox 트랜잭션으로 ENDED를 확정한다. 다른 회차·방송에는 영향을 주지 않는다.
5. never-sent 작업은 dispatch와 stop을 직렬화한 DB 증거가 있을 때만 별도 종료 경로로 허용한다. 기존 미귀속 작업에 새 epoch를 소급 대입하지 않는다. 구형 미완료 데이터는 안전하게 STOPPING에 남기며 별도 검증 가능한 운영 절차를 문서화한다.
6. 마이그레이션은 기존 데이터를 보존하고 귀속 필드의 완전성·불변성을 제약한다. 필요한 완료 증거 필드와 구형 자료 처리 정책은 07-A1 결과를 반영해 구현 전 확정한다. 실험 장벽은 제품 설정에서 비활성화하고 전용 검증 환경에서만 활성화한다.

## 추가 계약 결정

전송한 작업의 최종 closed 기록은 검증된 증거를 조건으로 하는 단일 서비스 경로로 모은다. ENDED 전이 트랜잭션에서도 전체 작업·generation·실행 종료 조건을 재검사한다. 공개 DELETE의 응답이 종료 요청 수락인지 실제 완료인지 API 문서와 클라이언트 동작을 일치시킨다. 기존 closed 이력을 새 계약으로 검증된 기록으로 재분류하지 않는다. 기존 migration 파일을 수정하지 않고 새 migration을 추가한다.

## 07-A1 계약을 제품에 연결할 때 확인할 항목

- 고정 패치의 내부 응답에는 `contract_version`, `root`, `slot`, `epoch`, `epoch_sequence`, `operation`, `state`, `registered`, `sequence`가 있다. 클라이언트는 누락·범위 밖 값·요청과 다른 tuple을 오류로 취급한다. `never-registered`, `closing`, `active`, `unknown_epoch`, `capacity_unknown`는 종료 증거가 아니다. `completed`만 해당 tuple에 대한 후보 증거다.
- 새 작업은 검증된 lifecycle의 tuple과 실제 전송할 SDP SHA-256을 원본 operation ID 및 경로와 함께 DB에 먼저 고정한다. `publish` HMAC은 `root`, `slot`, `epoch`, operation ID, 정확한 경로와 SDP digest에 묶는다. `close`와 `query`는 저장된 epoch·operation ID로 서명한다. 관리 응답의 epoch는 조회 대상과 달리 현재 실행일 수 있으므로 계약에서 지정한 필드별 의미를 검사한다.
- 프로세스가 lifecycle 조회 직후 바뀌면 이전 epoch의 POST는 거절될 수 있다. 예약한 작업의 epoch를 현재 값으로 다시 쓰지 않고, 그 이전 epoch가 실제 이력에 있으며 종료됐다는 응답을 확인해 정리한다. 키·slot·root 불일치, 이력 손상, 빈 기존 볼륨과 관리 API 오류는 서비스 준비 실패 또는 STOPPING 유지로 처리한다.
- `MarkDispatched`와 Stop은 session 행 잠금으로 직렬화한다. Stop이 먼저 commit하면 dispatch를 시작하지 않는다. dispatch가 먼저 commit했으면 Stop 이후 저장된 tuple로 close를 재시도한다. close가 Begin보다 먼저 도착하면 늦은 POST를 봉쇄하고, 이미 진행 중인 생성은 완료 증거가 나올 때까지 추적한다. 네트워크 요청은 트랜잭션 밖에서 보낸다. 요청 컨텍스트 취소나 HTTP 4xx는 dispatch된 작업을 closed로 바꾸지 않는다.
- 기존 `attached` resource DELETE와 worker의 경로 킥은 물리 정리 수단이다. HTTP 200/204/404, 최신 목록 부재, 5초 타이머만으로 operation을 closed로 만들지 않는다. 작업 완료 응답을 검증해 DB에 귀속·증거를 먼저 저장하고, FFmpeg 프로세스 그룹 및 새 미디어 snapshot의 정리를 확인한 후 ENDED를 결정한다.
- 구형 기록에는 새 tuple을 소급 생성하지 않는다. 특히 요청이 시작된 구형 미완료 작업은 unknown으로 남겨 운영자가 별도 근거를 확보하게 한다. 새 마이그레이션은 기존 DB 자료와 현재 실행 서비스를 안전하게 보존해야 한다.

## 완료 기준과 검증

- 공개 HTTP API + 실제 PostgreSQL + 고정 패치 Linux 미디어 환경에서 검사한다. 구현 내부 helper만 호출한 테스트로 대체하지 않는다.
- dispatch commit 전 실패, commit 후 POST 이전 API 종료, POST 응답 유실, Attach commit 뒤 answer 유실, upstream 4xx 및 DELETE 200/204/404에서도 증거 전에는 closed/ENDED가 되지 않는다.
- 장벽으로 인증/세션 생성/AddPublisher를 정지하고 Stop과 경쟁시킨다. 지연이 5초를 넘어도 STOPPING을 유지하고 release 후 정확한 완료 증거로 복구한다. 이전 응답이 새 회차를 LIVE로 바꾸지 않는다.
- API/worker 재시작, close/query 응답 유실, MTX A→B→C 교체, 관리 API 장애·잘못된 계약·tuple 불일치·포화를 검증한다. 알 수 없는 결과는 fail-closed이며 운영 복구가 가능해야 한다.
- 정상 방송 B의 ingress, FFmpeg PID/시작 시각, 연속 영상·음성을 A 종료 중 확인한다. 실제 FFmpeg 종료 및 새 자원 조회 뒤에만 A가 ENDED인지 확인한다.
- 기존 인증·인가·CRUD·영속성·PC 화면과 두 음원·카메라·익명 시청 회귀를 실행한다. 실제 휴대폰/외부망/Google/VAAPI/ABR 관문은 별도로 남긴다.

07-A2 완료 전 부모07-A 및 07-B의 선행조건을 충족했다고 표시하지 않는다. 새 제품 구현자나 병렬 편집을 시작하지 않는다.

## 2026-11-11 제품 체크포인트

지정 단독 구현자가 제품 API·DB migration·worker와 공용 완료 client를 연결하고 전용 HTTPS/Flutter/MediaMTX 검증 스택을 작성했다. 조정자는 실제 PostgreSQL·두 Chrome의 WHIP/HLS·두 음원·종료 근거·FFmpeg/ingress 부재를 독립 확인했다. 두 축 리뷰에서 발견한 자연 FFmpeg 종료와 DB 전이 실패 경합, lifecycle 일반 `sequence` 타입, URL 뒤 `/` 설정 불일치를 수정했다. 자연 종료의 기록 실패 및 전이 실패 재시도는 각각 별도 실제 브라우저 검사로 통과했다. 실행 세부는 `docs/verification/07a2-media-completion-integration.md`, 리뷰는 `docs/reviews/07a2-media-completion-integration.md`를 따른다.

남은 관문은 제품 설정의 등록·인증·AddPublisher·RTP barrier와 MediaMTX A→B→C 교체 전체 매트릭스, 구형 미완료 작업의 운영 복구 경로다. 실제 iPhone·외부망·평가 로그인은 09-A에서 인수한다. 구현자 편집은 독립 검증 동안 중단됐다.

## 구현 착수 설계 검토

2026-11-11 지정 단독 구현자는 새 migration의 고정 operation 귀속과 별도 완료 증거, 공용 미디어 client의 HMAC·lifecycle·응답 tuple 검증, API의 commit 전송 순서, Stop과 dispatch의 session 직렬화, worker의 정확한 close/query→FFmpeg 종료→최신 snapshot→ENDED 재검사를 보고했다. 조정자는 family→broadcast→session 잠금 순서, never-sent와 dispatch 경쟁, 구형 closed 비재분류, unknown/4xx/timeout fail-closed를 조건으로 범위 내 계속 구현하도록 전달했다. 이 설계 보고는 제품 동작이나 테스트 통과 결과가 아니다.

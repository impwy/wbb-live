# 07-A2: 종료 완료 계약의 제품 통합

**Status:** needs-info
**Blocked by:** 07-A1 실제 Linux 미디어·장애 매트릭스, 독립 검증 및 ADR0001 채택. 현재는 설계 초안이며 구현 시작 승인이 아니다.
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

## 완료 기준과 검증

- 공개 HTTP API + 실제 PostgreSQL + 고정 패치 Linux 미디어 환경에서 검사한다. 구현 내부 helper만 호출한 테스트로 대체하지 않는다.
- dispatch commit 전 실패, commit 후 POST 이전 API 종료, POST 응답 유실, Attach commit 뒤 answer 유실, upstream 4xx 및 DELETE 200/204/404에서도 증거 전에는 closed/ENDED가 되지 않는다.
- 장벽으로 인증/세션 생성/AddPublisher를 정지하고 Stop과 경쟁시킨다. 지연이 5초를 넘어도 STOPPING을 유지하고 release 후 정확한 완료 증거로 복구한다. 이전 응답이 새 회차를 LIVE로 바꾸지 않는다.
- API/worker 재시작, close/query 응답 유실, MTX A→B→C 교체, 관리 API 장애·잘못된 계약·tuple 불일치·포화를 검증한다. 알 수 없는 결과는 fail-closed이며 운영 복구가 가능해야 한다.
- 정상 방송 B의 ingress, FFmpeg PID/시작 시각, 연속 영상·음성을 A 종료 중 확인한다. 실제 FFmpeg 종료 및 새 자원 조회 뒤에만 A가 ENDED인지 확인한다.
- 기존 인증·인가·CRUD·영속성·PC 화면과 두 음원·카메라·익명 시청 회귀를 실행한다. 실제 휴대폰/외부망/Google/VAAPI/ABR 관문은 별도로 남긴다.

07-A2 완료 전 부모07-A 및 07-B의 선행조건을 충족했다고 표시하지 않는다. 새 제품 구현자나 병렬 편집을 시작하지 않는다.

# 07-A2 종료 완료 제품 통합 리뷰

상태: 제품 통합의 두 축 리뷰와 결함 수정 진행 중. 아래 처음 네 항목은 변경 전 코드에서 도출한 검토 기준이며, 뒤의 실행 결과와 구분한다.

## Spec

1. `livesessions.MarkDispatched`는 기존에는 operation만 갱신하고 JOIN된 session 상태를 읽었다. Stop의 session 행 잠금과 직렬화되지 않아, Stop이 먼저 commit한 뒤에도 POST가 시작될 수 있다. 새 구현은 family→broadcast→session 잠금 순서를 지키면서 dispatch와 Stop의 선후를 DB로 증명해야 한다.
2. 기존 `httpapi/media_handlers.go`는 upstream 4xx 또는 DELETE 200/204/404를, `mediaworker/worker.go`는 known resource의 5초 부재를 `closed`로 만들었다. 이미 dispatch된 작업에는 정확한 저장 tuple의 `completed` 또는 검증된 과거 epoch 종료만 증거로 허용한다. Location 없는 POST와 응답 유실도 같은 operation ID로 회수한다.
3. worker의 기존 snapshot→개별 closed 업데이트→unresolved 조회→별도 `ENDED` 전이에는 최종 작업·generation·FFmpeg·자원 근거 재검사가 없다. 최종 전이 트랜잭션의 가드와 다른 회차에 대한 fencing을 검토한다.
4. 기존 0005/0006 migration의 operation에는 epoch·slot·root·SDP digest가 없다. 새 migration은 과거 미귀속 작업과 과거 closed를 새 종료 근거로 소급 분류하지 않으며 기존 방송·인증 데이터를 보존해야 한다.

## Standards

MediaMTX 관리 서명은 `WBB_COMPLETION_KEY_HEX`를 32바이트로 해석한 키를 사용한다. 기존 `WBB_MEDIA_SECRET`의 Basic 자격과 용도·encoding을 혼동하지 않는다. 공용 미디어 client의 interface가 lifecycle/서명/응답 tuple 검증을 소유하고 API와 worker가 동일 구현을 사용해야 한다. `root`, `slot`, `contract_version`, epoch 및 sequence 불일치·관리망 timeout·포화/unknown은 종료 완료나 다른 실행으로의 자동 재귀속이 아니다. 실제 비밀·SDP·내부 서명은 로그/공개 응답에 남기지 않는다.

A1 서명은 각 UTF-8 part의 바이트 길이와 `:`를 이어 HMAC-SHA256 hex로 만든다. publish의 path는 앞 `/`와 뒤 `/whip`이 없는 실제 source 경로, digest는 전송 SDP의 SHA-256이다. 과거 epoch의 `completed`는 `registered=false`여도 유효하고 close 응답은 HTTP 200이다(현재 epoch close는202, query는200). lifecycle의 일반 operation `sequence=0`은 유효하며 `epoch_sequence`만 양수다. 반환 tuple의 필드 누락과 타입 오류는 거절해야 한다. 이 차이를 무시한 응답 검증은 재시작 후 정상 완료를 영원히 미완료로 만들거나 잘못된 종료를 허용할 수 있다.

완료 리뷰는 구현자의 동결 해시와 공개 HTTP·실제 PostgreSQL·고정 Linux 미디어 검증 이후 두 축으로 다시 수행한다.

## 구현 중 확인한 회복 경합

초기 worker 변경은 5초 부재로 operation을 closed 처리하는 경로와 함께, 이전 worker run의 `state='exited'` 영속 갱신도 제거했다. 새 `completionReady`는 모든 run이 exited여야 ENDED를 허용하므로 worker 재시작 후 inherited 이전 run이 실제로 정리돼도 상태가 running/starting에 남아 STOPPING이 지속될 수 있었다. 지정 구현자가 정확한 run ID/fence/generation의 종료 기록을 복구했고 worker 강제 교체 검사에서 검증했다.

## 구현 중 확인한 기동 순환

API의 `/health/ready`에 MediaMTX completion lifecycle 확인을 추가한 시점에 전용 Compose의 `mediamtx`가 `api: service_healthy`를 기다리면 깨끗한 기동은 API→MediaMTX→API 대기가 됐다. `api: service_started`로 낮추고 새 이미지의 clean down/up에서 lifecycle 및 최종 API readiness 200을 확인했다.

## 구현 중 확인한 DB 장애 회복 결함

전용 PostgreSQL을 중지한 상태에서 `stopAll`은 실제 FFmpeg를 정리한 뒤 `w.runs`에서 그 run을 삭제하고 `lostLease`만 남겼다. DB 복구 후 worker가 회차를 STOPPING으로 바꾸었지만, `confirmRecoveredRunExit`는 **같은 incarnation**의 run을 건너뛰므로 `media_worker_runs.state=running`이 영속됐다. 수정 후에는 로컬에서 검증한 실행 신원을 영속 기록 성공까지 보존하며 실제 PostgreSQL 중단·복구 검사를 통과했다(`/tmp/wbb-07a2-impl-db-restart-final.log`).

## 제품 변경 두 축 리뷰

기준 `b5b0ff7^..b5b0ff7`를 독립 두 축으로 검토했다. **Standards:** 확정된 위반 0건. 가족→방송→회차 잠금 코드와 세 검증 스크립트의 TLS pin 코드 반복은 낮은 우선순위의 유지보수 지적 2건으로 남았다. **Spec:** 자연 FFmpeg 종료 시 영속 `exited` 기록 전 in-memory run을 버려 STOPPING이 고착되는 결함, lifecycle 일반 `sequence` 타입을 presence만 확인하는 결함, 설정에서 허용한 completion URL 뒤 `/`가 client에서 거절되는 결함 3건을 확인했다. 문서에는 구형 작업 운영 절차 누락과 DELETE 상태코드 불일치 2건이 있었다.

지정 구현자는 자연 종료에서 정확한 실행 신원의 `exited` 기록을 먼저 수행하고, 이후 세션 전이가 실패해 재시도할 때 같은 신원에 한해서만 idempotent로 받아들이도록 수정했다. 조정자 독립 브라우저가 자연 종료·전이 한 차례 실패의 복구와 완료 근거, 실제 자원 부재를 확인했다. 일반 `sequence`는 `uint64`로 decode하며 잘못된 타입·음수·분수·overflow를 거절하도록 테스트했고 API readiness의 malformed 응답 검사도 추가했다. 공용 client는 빈 경로와 단일 `/`를 허용하며 구현자가 API/worker 동시 기동과 브라우저 검사를 통과했다. 구형 작업은 `docs/operations/legacy-media-operations.md`에 안전한 보류·분리 절차를 기록했고 `docs/design.md`의 DELETE 상태코드를 제품과 맞췄다.

제품 전체 barrier 및 A→B→C 재시작 매트릭스는 독립 통합 실행이 남았다. 구형 미완료 작업의 자동 복구 도구는 없다. 실제 기기·외부망 인수도 별도다. 이 범위를 검증 완료로 표시하지 않는다.

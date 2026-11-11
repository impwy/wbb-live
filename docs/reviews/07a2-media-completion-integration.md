# 07-A2 종료 완료 제품 통합 리뷰

상태: 구현 중. 아래는 변경 전 코드와 07-A1 계약에서 도출한 검토 기준이다. 새 제품 구현의 통과 결과가 아니다.

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

초기 worker 변경은 5초 부재로 operation을 closed 처리하는 경로와 함께, 이전 worker run의 `state='exited'` 영속 갱신도 제거했다. 새 `completionReady`는 모든 run이 exited여야 ENDED를 허용하므로 worker 재시작 후 inherited 이전 run이 실제로 정리돼도 상태가 running/starting에 남아 STOPPING이 지속될 수 있다. lock·lease·프로세스 신원·최신 자원 근거를 확인한 뒤 정확한 run ID/fence/generation으로 exited를 기록하는 회복 경로와 실제 재시작 검사를 지정 구현자에게 요청했다. 수정·재검증 전의 지적이다.

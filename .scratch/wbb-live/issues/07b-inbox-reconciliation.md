# 07-B: Kafka inbox와 영속 재조정 요청

**Status:** ready-for-agent
**Blocked by:** 07-C 전달(완료), 07-A 종료 완료 계약의 제품 통합·독립 검증(미완료).
**목표:** 중복·역순 이벤트가 상태를 되돌리지 않도록 영속 수신 기록과 실제 미디어 재조정을 연결한다. 현재 07-A1과 동시에 구현하지 않는다.

## 범위와 인터페이스

기존 media worker 프로세스에 소비 루프를 연결한다. 별도 상태 머신이나 서비스를 만들지 않는다. 기존 DB 주기 감시, 즉시 권한 폐기와 종료 경로는 Kafka에 의존하지 않는다. 근거는 `docs/design.md` §9, 기존 `consumer_inbox`, `mediaworker.Worker.Run/tick`이다.

- 작은 이벤트 수락 경계에서 envelope/schema/type, Kafka key와 broadcast ID, aggregate와 session ID의 일치를 검증한다. 이벤트의 state를 명령으로 실행하지 않고 PostgreSQL 최신 원장을 조회한다.
- 한 DB 트랜잭션에서 consumer_name/event_id inbox와 회차별 재조정 요청을 저장한다. 중복은 무변경이며 현재 DB version보다 오래된 이벤트는 stale로 처리한다. 현재 version과 일치하면 요청을 upsert한다. 미래 version, 존재하지 않는 회차, 식별자 불일치는 정상 적용하지 않고 안전한 사유와 함께 보존·격리한다.
- inbox의 processed는 영속 재조정 의무 수락이며 실제 미디어 종료가 아니다. 미디어 I/O는 DB 트랜잭션 밖에서 수행한다.
- DB commit 뒤 Kafka offset을 commit한다. partition별 순차 처리와 rebalance 취소를 설계하여 앞 레코드의 DB 실패를 뒤 offset으로 건너뛰지 않는다. 불명확한 commit 결과는 재전달과 inbox로 처리한다.
- 제한된 wake-up 신호로 기존 worker의 단일 조정 루프를 깨운다. runs/lostLease/absentSince는 소비 goroutine에서 직접 수정하지 않는다. 소비 오류가 worker.tick 오류로 전파되어 불필요한 stopAll을 유발하지 않아야 한다.
- 시작 시와 주기 tick에서 영속 미처리 요청을 조회한다. 요청 완료는 읽었던 요청 세대와 일치할 때만 기록하여 조정 중 새 요청을 지우지 않는다. 요청 완료는 재조정 한 번의 수행이며 STOPPING 후속 감시는 계속한다.
- 영구 형식 오류는 원본과 안전한 사유를 영속 격리한 뒤 진행한다. 정확한 항목의 재처리 명령을 제공하고 일시 DB 오류는 격리하지 않는다. 대기 건수·최고 연령·격리·중복·stale 결과를 확인할 수 있어야 한다. 민감 payload를 로그에 기록하지 않는다. 08 전체 관측성을 대신하지 않는다.

## 완료 기준

1. 공개 HTTP로 만든 실제 이벤트가 실제 Kafka→inbox→재조정으로 이어진다. 실험에서 주기 tick을 길게 설정하여 이벤트가 다음 tick 전에 실제 미디어 관측·조정을 유발함을 증명한다. inbox 삽입만으로 완료하지 않는다.
2. 중복, v3→v2, 과거 회차 이벤트 재생 중 새 회차의 권한·상태·미디어를 보존한다. 오래된 이벤트 때문에 LIVE 복귀, 새 회차 종료, 중복 상태 전이와 outbox가 생기지 않는다.
3. DB commit 전 종료, commit 후 offset 전 종료, offset 후 wake-up 전 종료를 구분해 재현한다. 재시작 후 영속 요청을 누락 없이 처리한다.
4. 조정 중 새 요청, DB 재시작, consumer 재할당, 실제 Kafka 중단에도 기존 종료·권한 폐기·주기 감시가 동작한다. 이벤트 폭주가 lease 갱신을 지연시키지 않도록 처리 상한을 검증한다.
5. 격리·정확한 재처리·일시 오류 재시도·종료 신호를 검사한다. 07-A의 완료 근거와 STOPPING 보존을 약화하지 않는다.
6. 실제 의존 서비스를 사용한 Go race/vet/build, 기존 미디어·권한 회귀, 두 축 리뷰와 조정자 독립 검증을 수행한다. README에 consumer group, offset과 DB 원자성, 격리·재처리 방법, 한계를 설명한다.

구체적인 group 이름, migration과 요청 세대 표현은 구현 전에 짧게 설계 보고한다. 사용자 답변을 기다리는 미정 사항은 아니며 기존 설계 안에서 조정자가 검토한다.

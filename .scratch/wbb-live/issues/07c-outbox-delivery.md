# 07-C: 방송 outbox의 Kafka 전달

**Status:** in-progress
**Blocked by:** 04-B1 상태·outbox 원자 저장과 정상/권한/종료의 로컬 독립 검증(완료).05-A 제품d762510/PR8은 로컬 독립 검증 완료이며 CI는 별도 확인한다.
**구현 담당:** 지정 대체 Codex 단독. 조정자는 설계·독립 리뷰·검증을 담당한다.

## 목표와 범위

이미 PostgreSQL에 원자적으로 기록되는 방송 회차 이벤트를 Kafka로 전달한다. Kafka 중단·재시작·중복 발행이 방송 종료와 권한 차단을 막지 않아야 한다. 기존 상태 전이/미디어 감독을 재구현하지 않는다. 부모07의 unknown POST 종결 근거와 inbox 소비는 별도 미완료로 유지한다. 이 하위 작업으로 전체 장애 복구 완료를 주장하지 않는다.

관련 설계는 docs/design.md §9, 현재 저장은 wbb-backend/internal/platform/db/session_events.go와 migrations/0005_broadcast_sessions.up.sql을 따른다. 기존 마이그레이션을 덮어쓰지 않고 필요한 변경은 새 마이그레이션으로 추가한다.

## Interface 계약

- 독립 Go 발행 프로세스/명령과 작은 outbox 전달 module을 둔다. 기존 API·media worker의 시작/종료 즉시 경로에서 Kafka를 기다리지 않는다. 구체 명령·설정·내부 claim 방식은 구현 전 짧은 설계로 보고한다.
- topic은 wbb.broadcast.events.v1, key는 broadcast_id. envelope는 event_id,type,schema_version,aggregate_id,aggregate_version,occurred_at,payload,traceparent다. 원래 event_id/version/occurred_at/payload를 보존한다. aggregate는 방송 회차이며 broadcast key와 혼동하지 않는다.
- broker 승인 후에만 published_at을 기록한다. 승인 후DB 기록 전 장애는 같은 event_id 재발행을 허용하는 at-least-once다. exactly-once를 주장하지 않는다. broker 미확인/DB 실패를 성공 처리하거나 이벤트를 삭제하지 않는다.
- 둘 이상의 발행자가 겹치거나 중간 종료돼도 미발행 항목이 영구 점유되지 않게 한다. 선택한 claim/lease/잠금 방식의 만료·재시도·늦은 이전 발행자 조건부 마킹을 설명하고 검사한다. 네트워크 지연이 상태전이 DB 접근을 장시간 막지 않게 한다. 중복/역순은 이후 inbox 소비가 처리할 계약이다.
- 일시적인 broker/DB 장애는 제한된 backoff·jitter·종료 가능한 context로 재시도한다. 일시 장애를 횟수만으로 poison으로 버리지 않는다. 영구 형식/크기 오류는 근거 코드와 함께 격리하고 원본을 보존한다. 운영자가 정확한 event_id를 지정해 재처리할 수 있는 로컬 명령을 제공한다. 공개 관리HTTP는 추가하지 않는다.
- attempts, 대기 건수/가장 오래된 이벤트 나이, 격리 건수/안전한 오류 코드와 재처리 결과를 확인 가능하게 한다.08의 전체 OTel/LGTM 구현은 이 티켓에서 대신하지 않는다. 토큰·이메일·비밀번호·전체 민감 payload를 로그에 쓰지 않는다.
- 실제 Kafka image/version과 Go client를 고정하고 공식 문서로 지원 설정을 확인한다. 기존 PostgreSQL/Redis 및8090/미디어 스택을 보존한다. Kafka와 검증 자원은 WBB 전용이며 호스트 공개 포트는 필요할 경우loopback에 한정한다. 아직 외부배포하지 않는다.

## 완료 기준과 검증

1. 공개HTTP로 실제 회차를 준비/종료한 상태 변화와 outbox 이벤트를 연결하고, 실제Kafka consumer로 key/envelope/event_id/version을 확인한다. 임의 DB 삽입만으로 정상제품흐름을 대체하지 않는다.
2. 실제Kafka 중단 중에도 기존권한차단·미디어종료가 성공하고 이벤트가DB에남는다. 재개 후 누락없이전달되는지 확인한다. 활성 방송과 자원 변경은 전용스택에서 직렬화한다.
3. broker 승인 후DB마킹실패/프로세스종료를 구분해 재현하고 동일 event_id 중복전달·최종마킹을 확인한다. 실제프로세스중단과 모의장애주입을 명확히 기록한다. 내부 구현을 복제하는 테스트 대신 module Interface와 실제의존서비스를 사용한다.
4. 다중발행/claim소유자소실/늦은마킹과DB재시작을 검증한다. 배치의 한 영구실패가 다른방송의 정상항목 전달을 영구차단하지 않아야 한다.
5. 영구실패 격리·정확한항목 재처리, transient 장애 비격리, 정상 종료 신호와 재시작 후재개를 확인한다.
6. 변경에 적합한 Go race/vet/build 및 기존서버 회귀를 실행한다. 실행README·환경변수 예시·장애 재현 명령과 제한을 보고한다. 조정자가 두 축 리뷰와 독립 검증한다.

## 명시적 미완료

unknown WHIP POST 자동종결은 현재실행식별자 조회만으로 증명되지 않는다. 이전프로세스종료의 독립증거와 생성인증 fencing 설계가 필요하며 현재 STOPPING 보존을 약화하지 않는다. inbox 소비·회차version에 따른재조정, 실제iPhone/외부망/Google/VAAPI/ABR,08관측성과09배포인수도 별도 범위다.

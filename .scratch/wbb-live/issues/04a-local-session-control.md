# 04-A: 로컬 방송 준비·취소와 영속 상태

**Status:** done
**Blocked by:** 01(done). 사용자 승인으로02 실제 인수 전 로컬 구현 가능.
**상위:** 04-secure-pc-live. 지정 대체 Codex 단독 구현, 조정 Codex 독립 리뷰.

## 목표와 범위

로그인한 소유자가 Flutter 방송 상세에서 회차를 준비하고 취소하며, 방문자가 실제 상태를 조회한다. 실제 WHIP/영상은04-B 후속이며 이 단계에서 LIVE나 송출 완료를 표시하지 않는다. docs/design.md의 API·인증·상태 계약을 따른다.

- 실제 PostgreSQL에 회차, 멱등 요청, grant 발급/폐기 정보, 상태 outbox를 영속 저장한다. Kafka 전송은07이다. 방송당 활성 회차 유일 제약과 방송 행 잠금으로 시작/삭제 경합을 처리한다.
- POST sessions, GET sessions/current, POST stop, POST publish-grants 계약을 구현한다. grant 초기30초, 준비 제한60초, 상한120초를 일관되게 적용하고 재응답·family·갱신 조건을 검사한다. 원문 token을 로그/일반 저장에 남기지 않는다.
- STARTING→취소/준비 만료→STOPPING→자원 부재 확인→ENDED. 미디어 생성이 아직 연결되지 않은04-A는 ingress/변환 생성 자체가 불가능한 범위에서만 자원 부재를 판정한다. 후속 미디어 어댑터 연결 시 실패/unknown을 부재로 취급하지 않는다.
- 준비 만료·정리는 실제 프로세스에서 재시작 후에도 진행하며 GET 부수효과나 테스트 전용 clock 호출만으로 구현하지 않는다. 폐기된 family가 신규 회차를 만들지 못하도록 시작/폐기 경합을 직렬화한다.
- logout와 refresh 재사용 폐기 모두 family 폐기+해당 회차 STOPPING을 동일 트랜잭션으로 수행한다. 정상 refresh는 회차를 유지한다. 다른 로그인 family의 동일 owner는 stop 가능하다.
- Flutter 소유자 준비/취소, 공개 상태 조회·갱신, 오류 안내, 활성 회차 삭제 거절을 연결한다. 준비 버튼은 실제 송출 시작이라고 오인하지 않도록 표시한다. 사용할 수 없는 재생 버튼을 노출하지 않는다.
- 기존 인증/Google/CRUD 보존.8090 실험 및 기존 미디어 컨테이너 유지. 격리 DB/Redis·포트로 검사한다.

## 완료 기준과 검사

공개 HTTP+실제 PostgreSQL로 익명401/타인403/없는 방송404, 멱등 동일 키·다른 입력409·다른 family403, 동시 시작/삭제, 활성 삭제409, grant 만료·갱신, 정상 refresh, 로그아웃/재사용 폐기, 준비60초 초과 및 API/DB 재시작 영속성을 확인한다. STOPPING을 건너뛰어 미해결 자원을 종료로 확정하지 않는다. 공개 응답/로그에 자격·내부 주소가 없는지 확인한다. Flutter 사용자 동작 검사, analyze/test/web build, Go vet/race 검사를 수행한다. 실패→수정→통과 근거와 변경 목록·자원·잔여 문제를 보고하고 편집 중단 후 독립 리뷰를 받는다. 실제 미디어·실기기·외부망 인수는 이 단계 완료로 주장하지 않는다.

## 결과

지정 대체 Codex 구현 및 두 축 독립 리뷰 완료. 독립 Go race/vet, Flutter36개/analyze/Web 빌드, 별도 실행 API+실제 PostgreSQL/Redis17개 검사를 통과했다. 실제 API/DB 재시작과 Redis 장애 중 실제 준비 제한 만료도 확인했다. 상세는 `docs/reviews/04a-local-session-control.md`, `docs/verification/04a-local-session-control.md`.04-B 미디어 송출·LIVE·시청과 실제 기기 인수는 미완료다.

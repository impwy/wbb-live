# 08: 방송 실패를 전용 LGTM에서 추적

**What to build:** 운영자가 시작·시청·종료 실패를 API부터 worker·미디어까지 따라가고 자원 부족을 확인한다.

**Blocked by:** 07 장애 복구와 이벤트.

**Status:** ready-for-agent

**실행 가능 여부:** 선행 미완료로 blocked. 기본 구조화 로그와 계측 hook은 이전 티켓에서 마련할 수 있다.

- [ ] 전용 OTel·Alloy·Loki·Grafana·Tempo·Mimir에 API/방송/미디어/인프라 네 대시보드를 구성한다.
- [ ] 실제 시작 요청→DB→outbox→Kafka→worker의 context 연결과 로그 traceId 조회를 확인한다.
- [ ] player 최초 재생·오류·rebuffer의 제한된 이벤트를 수집하고 공개 OTLP를 노출하지 않는다.
- [ ] 민감 header/body/query와 고카디널리티 metric label을 검사한다.
- [ ] logs/traces3일·metrics7일 초기 retention, 큐·저장·메모리 상한과 관리망 접근을 설정한다.
- [ ] telemetry 저장소 중단이 로그인/송출을 막지 않고 drop count로 보인다. sampling을 모든 오류 trace 보장으로 설명하지 않는다.

**검증:** 실제 dashboard query와 장애 재현. 설정 파일 존재만으로 수집 성공으로 처리하지 않는다.

**결과:** 아직 미실행.

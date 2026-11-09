# 07: 장애 후 유령 방송 없이 상태 복구

**What to build:** 방송자가 탭을 닫거나 API·worker·미디어·Kafka가 재시작되어도 시청자는 실제 상태를 보고, 종료 작업은 중복 없이 끝난다.

**Blocked by:** 04-B1/04-B2 정상·권한·종료 인터페이스의 로컬 독립 검증(완료). 실제 기기 인수는 별도 관문이다.

**Status:** in-progress

**실행 가능 여부:** 07-C outbox 전달은 제품90921dc/PR9와 독립 검사·CI 통과로 완료했다. 07-A1 종료 증거 스파이크를 지정 단독 구현자가 진행한다. inbox 소비·재조정과 제품 종료 통합은 미완료다.04-B의 미해결 생성 작업을 이 티켓이 담당하므로04-B 전체 완료를 다시 선행 조건으로 요구하지 않는다.

- [ ] 상태와 outbox를 DB 트랜잭션으로 쓰고 Kafka의 중복·역순 소비를 inbox와 version으로 무해하게 처리한다.
- [x] Kafka 중단 중에도 종료 권한 차단·미디어 종료가 수행되며 재개 후 outbox를 재처리한다.07-C의 서로 구분된 실제 미디어 중단 검사와 HTTP/consumer 복구 검사를 근거로 한다.
- [ ] worker lease와 reconciler가 미디어 실제 상태를 확인하고 과거 회차 이벤트로 새 회차를 종료하거나 LIVE로 되돌리지 않는다.
- [ ] 탭 종료·15초 ingress 유실·준비60초 초과·종료 API 실패를 재현하고 UI에 실제 상태가 반영된다.
- [ ] PostgreSQL·API 재시작 후 메타데이터를 보존하고 미디어 연결·변환을 안전하게 조정한다.
- [ ] Redis 장애·캐시 삭제 실패가 폐기된 인증을 허용하지 않고 내부 종료 작업은 계속된다.

**검증:** BCAST-03/04, AUTH-01/03. 실제 의존 서비스 중단과 모의 장애 주입을 구별한다.

**결과:** 07-C 전달은 완료. 부모07의 known/unknown 종료 완료 증거, inbox 소비와 나머지 장애 인수는 미완료.

## 04-B에서 이어지는 미해결 생성 작업 회수

WHIP POST를 전송했으나 자원 주소를 얻지 못한 작업은 시간 경과만으로 closed/ENDED로 만들지 않는다. 고정 MediaMTX의 kick/목록 부재는 비동기 취소 처리 전체의 완료 증거와 같지 않다.04-B는 송출 권한을 폐기하고 관측된 회차 연결·변환을 정리하되, 검증할 수 없는 생성 작업은 STOPPING으로 남긴다.

- [ ] 응답 유실과 API 재시작으로 자원 주소를 기록하지 못한 작업을 실제 장애로 재현한다.
- [ ] 해당 작업의 상류 수명 종료를 확인하는 재시작 incarnation 등 독립 근거를 설계하고, 다른 회차를 중단하지 않는 자동/운영 복구 계약을 구현한다. 단순40초 경과나 관리자 임의 DB 상태 변경을 근거로 삼지 않는다.
- [ ] 근거가 없으면 UI는 종료 처리 중/복구 필요 상태를 정직하게 표시한다. 다음 회차를 허용하기 위해 unresolved 작업을 버리지 않는다.
- [ ] 근거 확보 후 정확한 과거 회차만 회수하고 메타데이터와 outbox를 보존한다. 중복 복구와 늦은 응답이 새 회차에 영향을 주지 않음을 검증한다.

이 후속 항목을 기록했다고04-B의 응답 유실 회수가 완료된 것은 아니다. 전체 필수 기능 완료 판정에는 이 미완료 경계를 다시 확인한다.

### incarnation 설계 조사 후보

고정 MediaMTX v1.21.3의 GET /v3/info는 프로세스 초기화 때 한 번 생성되는 started 값을 제공한다([core.go](https://github.com/bluenviron/mediamtx/blob/v1.21.3/internal/core/core.go#L50), [API](https://github.com/bluenviron/mediamtx/blob/v1.21.3/internal/api/api.go#L386-L390)). 별도UUID도구를 먼저 만들기보다 이 기존 API의 실제 값을 독립 확인한다.

단순 started 변경만으로 이전 실행 종료를 추론하지 않는다. 대상 인스턴스/실행 슬롯과 생성 작업 귀속을 영속 저장하고, info=A 조회 뒤 POST가 B에 도착하는 경합을 생성 자격/인증 fencing으로 차단해야 한다. Compose 동일 실행 슬롯의 자연 재시작과 컨테이너 교체/복제는 구분한다. k3s의 Service 로드밸런싱·rolling overlap·강제삭제·노드단절에서는 이전 실행 종료 증거가 별도로 필요하다. 이 설계는 후보이며 실제 구현·검증 전이다. 전역 재시작이나 Docker socket 노출을 요구하지 않는다.


## 로컬 구현 단계 검토

현재 코드에는 상태와 outbox의 원자 기록(`internal/platform/db/session_events.go`), 준비/ingress 감시와 lease, 실제 자원 종료 확인(`internal/mediaworker/worker.go`)이 있다. 이를 중복 구현하지 않고 다음 경계를 완성한다. 경로는wbb-backend 기준이며 아래는 계획이다.

1. **생성 작업 귀속과 종료 근거:** `internal/livesessions/publish.go`의 생성 예약·전송 표시에 실행 슬롯/incarnation 귀속이 없고 `internal/httpapi/media_auth.go`도 그 귀속을 인증하지 않는다. info=A 조회 후 POST가B에 도착하는 경합을 먼저 해결한다. 실제 응답 유실/API 종료, A→B 경합, 실행 교체/중첩을 검증한다. 기존 주소 없는 작업에 나중에 관측한started를 소급 대입하지 않는다.
2. **증명된 과거 작업 회수:** 현재worker가 주소 없는 전송 작업을 미해결로 남기는 안전성을 보존한다. 독립적인 상류 실행 종료 근거·생성 권한 폐기·새 자원 조회·변환 종료를 모두 확인한 정확한 작업/회차/generation만 종결한다. 늦은201·중복 복구·새 회차와의 경합을 검증하고, 별도 정상 방송의 ingress/PID/재생 유지를 확인한다. 전역 재시작이나Docker socket을 복구 수단으로 강제하지 않는다. 근거가 없으면STOPPING을 유지한다.
3. **outbox 전달:** 기존 이벤트 저장 위에 설계§9의topic/key/envelope를 따르는Kafka 발행·재시도를 연결한다. broker 확인 후DB 마킹 전 종료는 동일event_id 중복 발행으로 처리한다. 실제Kafka 중단 중에도 종료 권한 차단/미디어 종료가 작동하고, 재개 후 누락 없는 전달·오래된 미발행 탐지·격리/재처리를 확인한다.
4. **inbox와 재조정:** 이벤트를 즉시LIVE/ENDED 명령으로 적용하지 않는다. 현재DB 상태와 실제미디어를 다시 확인하는 기존 조정 경계를 사용한다. inbox와 소비DB 변경의 원자성, 회차별version, 중복·역순·과거 회차가 새 방송에 영향을 주지 않음을 검증한다. Kafka 없이도 주기 감시가 유지되어야 한다.

실제PostgreSQL/MediaMTX/FFmpeg/Kafka 장애 시험은 모의 장애와 구분한다. 실제 두 물리 기기·외부망·VAAPI/ABR 인수는 이 로컬 단계로 대체하지 않는다. 설계 조사만 완료했으며 위 단계의 구현·검증 완료를 의미하지 않는다.


현재실행: 07c-outbox-delivery.md. 기존상태감시를중복구현하지않고이벤트전달을완성한다. unknown생성작업회수와inbox소비는미완료다.


### callback UUID만으로 종결할 수 없는 근거

고정MediaMTX v1.21.3 공식소스 읽기전용 조사 결과다. 실제장애재현과 구분한다. publish auth callback의 UUID를 허용 전에 영속 기록하면 정확한 세션을 추적할 수 있지만 Location의 secret과는 달라 WHIP DELETE 주소를 복원하지 못한다. 관리 kick은 map 삭제 후 context 취소만 하고 종료 goroutine을 기다리지 않는다([server.go](https://github.com/bluenviron/mediamtx/blob/v1.21.3/internal/servers/webrtc/server.go#L467), [Close](https://github.com/bluenviron/mediamtx/blob/v1.21.3/internal/servers/webrtc/session.go#L299)).

구체적인 반례: ToStream 성공 직후 지연→kick/map삭제/관측부재→재개한 goroutine이 context 확인 없이 SkipAuth로 AddPublisher→이후취소를읽고정리할 수 있다([session.go](https://github.com/bluenviron/mediamtx/blob/v1.21.3/internal/servers/webrtc/session.go#L498)). 따라서 정확한UUID의404와당시path부재도향후늦은등록불가의증거가아니다. callback UUID 영속화는추적개선후보이지unknown종결구현의대체물이아니다. 독립실행종료증거 또는 동등한완료증명계약이없으면STOPPING을보존한다.


known Location 경로도강제삭제후5초부재만으로늦은생성불가능을증명하지못하는공통경합을발견했다. 07a-media-completion-evidence.md에근거와완료기준을분리했다. 정상실행검증성공을모든경합보장으로확대하지않는다.

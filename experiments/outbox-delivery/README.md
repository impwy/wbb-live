# 07-C 로컬 Kafka 전달 검증

이 구성은 WBB 전용 단일 Kafka broker를 loopback `127.0.0.1:19092`에만 연다. 운영 배포 구성이 아니다. 단일 broker의 `acks=all`은 다른 복제본의 내구성을 제공하지 않는다. 기존 API·미디어 worker는 이 broker에 의존하지 않는다.

발행기는 새 `0007_outbox_delivery` 마이그레이션이 적용된 DB에 연결한다. 기존 바이너리의 readiness가 최신 마이그레이션 버전을 비교하므로 기존 실행 중인 API의 DB에 발행기만 먼저 적용하지 않는다. 새 API 배포·마이그레이션 뒤 독립 발행기를 기동한다.

```sh
docker compose -p wbb-outbox-07c -f experiments/outbox-delivery/compose.yml up -d
docker compose -p wbb-outbox-07c -f experiments/outbox-delivery/compose.yml exec kafka /opt/kafka/bin/kafka-topics.sh --bootstrap-server localhost:19092 --create --topic wbb.broadcast.events.v1 --partitions 3 --replication-factor 1
```

`wbb-backend`에서 `WBB_DATABASE_URL`(전용 PostgreSQL 연결 문자열), `WBB_KAFKA_BROKERS=127.0.0.1:19092`를 프로세스 환경으로 주고 `go run ./cmd/wbb-outbox-deliver run`을 실행한다. `run`은 DB가 잠시 중단되어도 취소 가능한 재시도를 한다. `stats`는 대기·격리·시도 횟수와 가장 오래된 대기 시간을 JSON으로 출력한다. `quarantine`은 최근 격리 항목 최대 100개의 event ID·오류 코드·시도 횟수·시각만 출력하고, `quarantine <event_id>`는 오래된 항목도 정확히 조회한다. 전체 대상 ID가 필요하면 아래 SQL을 전용 PostgreSQL에서 읽기 전용으로 실행한다. `requeue <event_id>`는 정확한 미발행 격리 항목만 재처리한다. 연결 문자열과 이벤트 payload는 로그에 기록하지 않는다.

```sql
SELECT event_id, last_error_code, attempts, occurred_at
FROM outbox_events
WHERE quarantined_at IS NOT NULL AND published_at IS NULL
ORDER BY quarantined_at DESC, event_id;
```

broker 중단은 `docker compose -p wbb-outbox-07c -f experiments/outbox-delivery/compose.yml stop kafka`, 재개는 같은 명령의 `start kafka`로 한다. 이때 API의 방송 종료/권한 차단이 계속되는지 따로 검사한다. 전용 자원을 제거할 때는 `down`을 쓰며, 검증 데이터까지 삭제할 때만 전용 project의 `down -v`를 쓴다. 원래 미디어·8090 project에는 이 명령을 적용하지 않는다.

## 전달·장애 회귀 실행

테스트용 PostgreSQL 관리자 URL은 **삭제 가능한 격리 서버**를 가리키게 한다. 테스트는 무작위 `wbb_outbox_test_...` DB를 만들고 종료 때 삭제한다. 실제 비밀번호를 명령 기록이나 문서에 넣지 말고 환경으로 전달한다.

```sh
cd wbb-backend
WBB_TEST_PG_ADMIN_URL="$TEST_ADMIN_URL" WBB_TEST_KAFKA_BROKERS=127.0.0.1:19092 \
  go test -race -count=1 -v ./internal/outboxdelivery
WBB_TEST_PG_ADMIN_URL="$TEST_ADMIN_URL" WBB_TEST_KAFKA_BROKERS=127.0.0.1:19092 \
  go test -race -count=1 -run '^TestOutboxPublicSessionToKafka$' -v ./internal/httpapi
```

첫 검사는 실제 broker 레코드별 승인, ACK 직후 DB 마킹 실패 주입, ACK 직후 자식 발행 프로세스 강제 종료와 동일 event ID 중복, 1초 lease 만료 재획득, 동시 8개 claim의 중복 방지, 이전 토큰의 늦은 성공·실패·격리 거절, 101건 격리 중 과거 ID 조회와 정확한 미발행 재처리를 확인한다. 실패 주입은 실제 DB 장애가 아니며, 중복이 허용되는 at-least-once 경계를 증명한다. 두 번째 검사는 공개 HTTP 회차에서 생긴 이벤트를 실제 consumer가 key·ID·version으로 수신하고 해당 3건의 DB 마킹을 확인한다.

실제 broker 중단은 전용 Kafka만 멈춘 다음 두 터미널에서 실행했다. 첫 발행 시도가 미확인으로 끝난 **뒤** broker를 재개해야 한다.

```sh
# 터미널 1: 저장소 루트
docker compose -p wbb-outbox-07c -f experiments/outbox-delivery/compose.yml stop kafka
# 터미널 2: wbb-backend
WBB_TEST_PG_ADMIN_URL="$TEST_ADMIN_URL" WBB_TEST_KAFKA_BROKERS=127.0.0.1:19092 \
  WBB_TEST_KAFKA_OUTAGE=1 go test -race -count=1 -timeout 90s \
  -run '^TestOutboxPublicSessionDuringKafkaOutage$' -v ./internal/httpapi
# 터미널 1: 첫 미확인 시도 이후
docker compose -p wbb-outbox-07c -f experiments/outbox-delivery/compose.yml start kafka
```

이 검사는 broker가 멈춘 상태에서 HTTP 준비·중지·ENDED와 미발행 3건을 확인한 뒤, 재개 후 마킹과 consumer 수신을 확인한다. 실제 미디어 종료는 별도 전용 Chrome 카메라 검증으로 확인한다. `verify-browser.mjs`의 직접 API 인증은 Flutter overlay가 동시에 refresh를 회전시키면 이전 access token으로 중지 요청이 401일 수 있으므로 제품 화면 검증에는 `verify-flutter.mjs --camera`를 사용한다.

DB 시작 장애/재개와 SIGTERM 후 재개는 **별도 일회용 PostgreSQL 컨테이너**와 전용 DB에서만 검사한다. 기존 미디어 PostgreSQL 컨테이너를 재시작하지 않는다. 이 테스트는 무작위 전용 DB를 만들고, 초기 DB 중단 상태에서 발행기를 시작해 복구를 확인하고, 미래 재시도 시각을 가진 미발행 이벤트가 있는 상태에서 TERM·종료 코드0·재기동 전달을 확인한다. 이후 발행기가 살아 있는 중 DB를 재시작해 다음 이벤트 전달을 확인한다.

```sh
# 저장소 루트: 전용 loopback 컨테이너만 생성한다. --rm을 쓰면 stop 뒤 재시작할 수 없다.
docker run -d --name wbb-outbox-07c-pg -e POSTGRES_USER=wbbtest \
  -e POSTGRES_DB=postgres -e POSTGRES_HOST_AUTH_METHOD=trust \
  -p 127.0.0.1:55439:5432 postgres:16-alpine
cd wbb-backend
go build -o /tmp/wbb-07c-deliver ./cmd/wbb-outbox-deliver
WBB_TEST_PG_ADMIN_URL=postgres://wbbtest@127.0.0.1:55439/postgres \
  WBB_TEST_KAFKA_BROKERS=127.0.0.1:19092 \
  WBB_TEST_PG_RESTART_CONTAINER=wbb-outbox-07c-pg \
  WBB_TEST_OUTBOX_BINARY=/tmp/wbb-07c-deliver \
  go test -race -count=1 -timeout 90s -run '^TestOutboxProcessTermAndDatabaseRestart$' -v ./internal/outboxdelivery
docker rm -f wbb-outbox-07c-pg
rm /tmp/wbb-07c-deliver
```

`POSTGRES_HOST_AUTH_METHOD=trust`는 이 **일회용 loopback 전용** 실험에서만 사용한다. 운영 DB에 적용하지 않는다. 실제 DB 컨테이너 재시작과 TERM 재개는 이 자동 검증에서 통과했으며, ACK 후 종료 경계는 별도 자식 발행 프로세스를 강제 종료해 확인한다. 전체 Kafka/DB 동시 장애나 다중 broker 내구성은 이 검사로 증명하지 않는다.

고정 버전과 설정 근거: [Apache Kafka 4.4 Docker 이미지와 단일 broker Compose 예시](https://kafka.apache.org/44/getting-started/docker/), [공식 단일 노드 Compose](https://github.com/apache/kafka/blob/4.4.0/docker/examples/docker-compose-files/single-node/plaintext/docker-compose.yml), [franz-go v1.22.3 릴리스](https://github.com/twmb/franz-go/releases/tag/v1.22.3), [franz-go 발행/승인 설명](https://github.com/twmb/franz-go/blob/master/docs/producing-and-consuming.md). 발행기는 `acks=all`과 **레코드별 callback 성공**만 DB 마킹 근거로 사용한다. 15초 delivery timeout과 취소 가능한 idempotent produce는 무응답을 유한하게 처리하지만, ACK 유실 또는 취소 뒤 재시도는 같은 event ID 중복을 만들 수 있다. 단일 broker의 승인에는 복제 내구성이 없다.

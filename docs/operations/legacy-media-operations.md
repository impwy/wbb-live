# 구형 미디어 작업의 안전한 운영 처리

대상은 07-A2 마이그레이션 이전에 생성돼 `media_create_operations.completion_legacy=true`인 작업이다. 당시 MediaMTX에는 root·slot·epoch·operation 완료 이력이 없었으므로, 예전 `closed` 상태나 Location·자원 목록 부재를 새 계약의 `completed` 증거로 해석할 수 없다. 이 절차는 새 방송의 정상 동작을 유지하면서 구형 자료를 보존하는 안전 경계다. **현재 릴리스에는 구형 작업을 새 증거로 승격하거나 구형 STOPPING 회차를 강제로 ENDED로 바꾸는 기능이 없다.**

## 확인과 격리

1. DB와 기존 미디어 설정·볼륨을 먼저 백업하고, 작업 ID·방송 ID·회차 ID·generation·현재 상태를 기록한다. 비밀번호·JWT·SDP·스트림 자격은 기록에 포함하지 않는다.
2. 읽기 전용으로 다음 작업을 확인한다. 새 계약의 tuple/proof가 비어 있는 것이 정상이며, 이를 채우는 UPDATE를 하지 않는다.

   ```sql
   SELECT o.id, o.broadcast_session_id, o.media_generation, o.state,
          o.request_started_at IS NOT NULL AS request_started,
          s.state AS session_state
   FROM media_create_operations AS o
   JOIN broadcast_sessions AS s ON s.id = o.broadcast_session_id
   WHERE o.completion_legacy
   ORDER BY o.broadcast_session_id, o.id;
   ```

3. 이전 API·worker·MediaMTX의 네트워크 송출 경로를 격리하고, 이전 프로세스가 실제로 종료돼 새 WHIP POST나 FFmpeg 생성이 더 이상 일어날 수 없음을 운영자가 확인한다. 현재 제품의 새 completion 이력 볼륨에는 이전 작업을 넣거나 기존 볼륨을 덮어쓰지 않는다.
4. 구형 미완료 작업이 있는 회차는 STOPPING으로 유지한다. 새 송출이 필요하면 소유자가 현재 제품에서 **새 방송**을 등록해 새 회차를 시작한다. 원래 방송과 백업은 감사·후속 복구를 위해 보존한다. 구형 작업이 존재한다는 이유로 다른 방송의 종료나 새 방송 생성을 막지 않는다.

## 금지된 지름길과 후속 복구

- `request_started_at IS NULL`만으로 구형 작업을 never-sent로 표시하지 않는다. 과거 Stop과 dispatch의 직렬화 증거가 없기 때문이다.
- 구형 `closed`를 새 `completed`로 재분류하거나 임의 epoch를 소급 기입하지 않는다. MediaMTX DELETE 200/204/404, 목록 부재, 일정 시간 경과만으로 회차를 ENDED로 직접 갱신하지 않는다.
- 구형 STOPPING 회차의 상태 변경이 실제로 필요하면, **별도 버전의 관리자 복구 도구**를 설계·검증해야 한다. 해당 도구는 이전 API/미디어 프로세스 종료와 미래 생성 차단, 회차·작업·프로세스 신원 및 현재 자원 부재를 독립 근거로 기록하고, 서버의 권한·outbox 상태 전이를 한 트랜잭션으로 수행해야 한다. 현재 릴리스에 그런 도구는 없으므로 수동 SQL로 우회하지 않는다.

새 제품 전용 볼륨은 고정 root·slot anchor로 빈 상태에서 한 번 명시적으로 bootstrap한다. 새 제품의 API·worker·MediaMTX에는 동일한 root·slot·별도 HMAC 키를 주입한다. 구형 작업은 이 새 이력에 자동 합류하지 않는다. 운영자는 구형 STOPPING 건수와 새 회차의 정상 종료를 분리해서 관찰한다.

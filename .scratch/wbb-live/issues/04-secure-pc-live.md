# 04: 소유자가 PC에서 시작하고 방문자가 시청·종료 확인

**What to build:** 로그인한 방송자가 방송 상세에서 화면·두 음원을 송출하고 비로그인 시청자가 다른 기기에서 시청한다. 소유자 종료 후 실제 연결과 변환이 멈춘다.

**Blocked by:** 01 이메일 인증·방송 관리, 02 단일 품질 미디어 검증.

**Status:** ready-for-agent

**실행 가능 여부:** 선행 미완료로 blocked.

- [ ] STARTING은 ingress와 출력이 준비된 이후 LIVE가 되고 실패/시간초과는 STOPPING을 거쳐 종료된다.
- [ ] grant는 회차·로그인 세션·owner에 묶이고120초 이내 신규 POST만 허용한다. 이후 PATCH/DELETE는 갱신 가능한 access JWT와 resource 소유권으로 처리한다.
- [ ] 안정된 로그인 식별자는 auth family이며 정상 refresh 후 제어가 유지된다. family 폐기 시 STOPPING, 다른 로그인 family의 동일 owner는 방송 stop API로 종료할 수 있다.
- [ ] grant 초기 TTL30초·회차 준비 제한60초·최초 ingress 이후 유실15초를 구분한다. 생성 작업을 영속 예약해 중복 POST, stop과 늦은201, upstream 응답 유실·API 재시작을 정리하고 미해결 작업이 있으면 ENDED를 금지한다.
- [ ] 시작 멱등 재응답은 같은 회차와 원래 유효 grant 또는 null+상태를 반환한다. 만료 grant 갱신은 같은 family·준비 제한 이전·미해결 생성 없음 조건을 검사한다.
- [ ] anonymous/다른 owner/다른 회차/종료된 회차의 publish 및 control을 서버와 MediaMTX 경계에서 거절한다.
- [ ] 중복 시작은 Idempotency-Key/활성 회차 유일 제약으로 보호하고 두 번째 publisher가 첫 연결을 교체하지 못한다.
- [ ] 공유 중지·종료 버튼·로그아웃으로 권한을 먼저 차단하고 실제 ingress·FFmpeg를 종료한다. 실패 시 STOPPING 재시도를 유지한다.
- [ ] 비로그인 시청·자동재생 안내·상태 표시·오류 재시도가 Flutter 화면에 연결된다.
- [ ] API 권한 검사와 실제 두 기기 PC 영상/두 음원/종료/이전 grant 재접속 거절을 각각 기록한다.

**검증:** PC-01~04, BCAST-02/03, AUTH-02/03.03 미완료라면 단일 품질로만 검증하고 ABR 미완료를 유지한다.

**결과:** 아직 미실행.

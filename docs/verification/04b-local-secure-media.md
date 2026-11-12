# 04-B1 로컬 인증 미디어 검증

기준 제품 `03ee70c` 이후 `work/local-secure-media`의 서버·worker·전용 로컬 환경을 검사했다. 기존 8090 helper와 CPU 미디어를 보존했다. Flutter 통합, 실제 기기·외부망 인수와 전체 필수 기능 완료 보고는 아니다.

## 조정자 독립 검사

| 범위 | 결과 | 증명하는 경계 |
| --- | --- | --- |
| Go 전체 `-race -count=1 ./...`, `go vet ./...` | 통과, 최근 HTTP 패키지 134.204초 | 실제 별도 PostgreSQL/Redis의 HTTP 인증·권한·회차·미디어 제어 회귀 |
| 늦은 201 검사 | 1.54초 통과 | 외부 성공 노출 거절, 정확한 상류 DELETE 수신 |
| 추가 응답 유실·클라이언트 취소 검사 | 수정 전 timeout → 수정 후 2.22초 통과, race/vet 통과 | unknown 작업 보존, 취소 관측, 유한 테스트 정리 |
| Linux worker 검사 | 9개 통과, skip 0 | 전체 페이지 조회·빈 목록, 실제 part 진행, query-only 변경 거절, 외부/타 회차 참조 거절, TERM·KILL, 잠금 상속, RTSP 경로 선택 |
| 실제 FFmpeg FD 검증 | 통과 | 부모 사망 뒤 자식 FD 잠금 유지, FFmpeg 종료 뒤 잠금 재획득 |
| HTTPS 외부 경계 | 8개 통과 | 검증된 로컬 CA, health 허용, 내부 인증/인가/metrics·직접 WHIP 차단, 없는 회차 HLS 거절 |
| 제품 worker SIGKILL | 통과 | 아래 실제 강제 종료·복구 결과 |
| 진행 중 LL-HLS 요청 종료 | 통과 | 아래 실제 blocking 요청 결과 |

전체 Go 검사 이후 변경은 응답 유실 테스트 fixture와 브라우저 검증 스크립트였다. 추가 fixture는 별도 새 DB에서 다시 검증했다. 조정자 임시 DB·Redis·볼륨·컨테이너는 정리했다. timeout 때문에 TestMain 정리가 생략된 Redis도 해당 실행의 소유 관계를 확인해 정리했다.

### 제품 worker 강제 종료·복구

구현자의 코드·스택 동결과 활성 회차 0개를 확인한 뒤 새 합성 송출 중 실제 worker PID에 SIGKILL을 보냈다. 컨테이너 RestartCount가 1 증가했고 새 fencing run·실제 FFmpeg로 복구됐다.

- 별도 Chrome 시청자가 복구 후 33프레임을 디코딩했다.
- 초기 440/660Hz 두 수신 피크는 -49.57/-49.91dB, 주변 대역은 -108.20dB였다.
- stop 202 후 7,286ms에 ENDED, 이전 playback 409·HLS 403을 확인했다.
- FFmpeg 종료와 해당 회차 ingress·RTSP 부재를 확인했다.

기존 플레이어의 재시작 중 무중단 재생이나 재시작 후 오디오 재청취까지 증명한 것은 아니다.

### 진행 중 blocking LL-HLS 요청

별도 새 회차의 FFmpeg를 SIGSTOP으로 일시 정지하고, 실제 media playlist의 sequence를 기준으로 다음 segment를 기다리는 요청을 시작했다. 요청이 400ms 뒤에도 실제 pending인 것을 확인한 후 stop을 보냈다.

- 이미 승인된 요청은 총 2,082ms 뒤 HTTP404로 끝났다. 클라이언트 자체 timeout으로 종료된 결과가 아니다.
- 신규 이전 회차 playback은409, HLS는403이었다.
- stop 후8,948ms에ENDED가 되었고 FFmpeg·ingress·RTSP가 남지 않았다.

임시 검증기 첫 호출은 macOS `/tmp` 심볼릭 링크와 main 진입 조건 차이로 실제 검사를 실행하지 않았다. 빈 exit0을 통과로 인정하지 않고 진입 조건을 수정한 뒤 위 실제 결과를 얻었다.

## 구현자 실제 미디어 검사

지정 구현 세션의 실제 도구 출력과 검증 코드를 조정자가 확인했다. 검증기는 두 Chrome 프로세스의 합성 canvas·440/660Hz와 익명 hls.js 재생을 사용한다.

| 실행 | 확인 결과 |
| --- | --- |
| 정상 송출·시청·종료 | 준비202, WHIP201, LIVE, 영상·두 음원, 종료후409/403, ENDED·자원 부재 |
| 다중 회차 오류·제어 지연 | 정상 회차의 LIVE·PID·lease·재생 유지. 지속 검사에서2457프레임/81.38초 진행, 최소 lease 잔여24,630ms, 관측된 stall0ms |
| worker 중지·재시작 | 실제 FFmpeg fd3와 lock inode 일치, 실행 중 추가 잠금 거절, 중지 뒤 잠금 획득, 새 fencing run과 새 시청자 재생 |
| API 재시작 | 송출 회차·worker를 유지하고 새 시청자 재생 및 최종 정리 |
| Redis 장애 | 보호 stop API503, 브라우저 연결 종료 뒤 worker가 Redis 없이 STOPPING→ENDED, 약34.3초, 이전 HLS403·playback409와 자원 부재 |
| MediaMTX 제어 장애·복구 | 장애를 실제 주입하고 복구 후 ENDED·이전 URL 차단·자원 부재 |

지속 검사 수치는 샘플링 관측이다. 샘플 사이 완전 무중단이나 10개 방송·500명 시청 용량을 보장하지 않는다. 모의 upstream 검사와 실제 서비스 중단 검사는 구분한다.

## 리뷰와 남은 범위

[두 축 리뷰](../reviews/04b-local-secure-media.md)의 최종 정적 잔여 확정 지적은 Standards0건/Spec0건이다. 회차 간 오류 전파, ingress 정리 순서, lease 만료와 갱신 간격을 수정하고 재검토했다.

주소를 받지 못한 unknown POST는 시간만으로 closed/ENDED 처리하지 않는다. 근거 있는 자동 회수는04-B/07에 남아 있으며, 재시작 성공 검사로 이 제한을 완료 처리하지 않는다. 서버 정상·권한 차단 인터페이스를 기반으로04-B2를 진행한다.

Flutter PC 통합, 모바일 제품 통합, 실제 PC 화면·공유 소리·마이크와 물리 두 기기, 외부망, Google 실제 자격, VAAPI/ABR, 운영 용량·공개 배포·평가 README는 별도 미완료다. 공개 서비스는 사용자 로컬 테스트 전환에 따라 다시 기동하지 않았다.

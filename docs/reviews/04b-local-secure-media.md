# 04-B1 인증 미디어 서버 리뷰

기준 제품은 `03ee70c`, 대상은 `work/local-secure-media`의 서버·worker·전용 로컬 검증 환경이다. Flutter 통합은 04-B2다. 제품 구현은 지정 대체 Codex, 리뷰·독립 검증은 조정 Codex가 담당했다.

## Standards

최종 정적 재검토의 잔여 확정 지적은 **0건**이다. 한 회차의 미디어 제어 실패가 모든 회차의 FFmpeg를 종료하던 경로를 수정했다. 회차별 오류와 공유 DB·전체 미디어 관측 실패를 구분하고, 개별 작업에 별도 처리 시간을 부여한다. 의미 있는 추가 Fowler 휴리스틱 지적은 없다.

## Spec

최종 정적 재검토의 잔여 확정 지적은 **0건**이다. 다음 지적을 수정하고 재검토했다.

- ingress 정리를 먼저 수행하고, FFmpeg 종료 실패가 다른 자원 회수 시도까지 막지 않도록 오류를 모은다. 정리를 확인하지 못하면 STOPPING을 유지한다.
- 공용 처리 시간 소진이 정상 회차로 전파되지 않게 회차별 시간을 분리했다. lease는 단일 제한시간 SQL로 갱신하며 incarnation/run/fence와 기존 만료 여부를 확인한다. snapshot·조회·회차 처리 사이에도 갱신한다.
- 정상 종료가 확인된 자신의 run은 정확한 run/fence를 조건으로 exited를 기록하여 불필요한 30초 lease 대기를 제거했다. 다른 worker의 불확실한 실행에는 이 경로를 적용하지 않는다.

두 축은 별도로 검토했으며 전체 필수 기능 완료 판정은 아니다. 실행 근거는 [검증 기록](../verification/04b-local-secure-media.md)을 따른다.

## 독립 검사에서 보완한 사항

실제 발급 JWT의 한 필드만 변조하는 검사와 정상 대조군, init과 실제 part 구분, 전체 페이지 조회, TERM 무시 준비 장벽, source/output RTSP 모두 정리, 유한 DB 정리 context를 보완했다. HLS master의 세션이 붙은 하위 playlist를 따라가며 인증 query 변경을 미디어 진행으로 오판하지 않는다. 외부·타 회차 참조는 내부 자격을 전송하기 전에 거절한다.

늦은 201의 외부 노출을 거절하고 해당 상류 resource의 DELETE를 시도한다. 응답 유실·클라이언트 취소는 주소 없는 작업을 미해결 상태로 보존한다. 이 테스트의 요청 body 미소비로 httptest 종료가 멈추던 fixture를 수정했고 조정자가 동일 검사에서 FAIL→PASS를 확인했다.

## 종료 근거와 남은 범위

FFmpeg는 고정 lock 파일의 동일 open-file-description을 상속한다. 명시적 LOCK_UN 없이 마지막 FD가 닫힐 때 잠금이 풀린다. 고정 이미지의 실제 FFmpeg 상속과 제품 worker SIGKILL·컨테이너 자동 재시작을 각각 검증했다.

MediaMTX v1.21.3은 신규 WebRTC session을 인증 전에 목록에 등록한다. 초기 리뷰의 “이미 인증된 session이 마지막 조회 뒤 처음 나타난다”는 가정은 확정 결함에서 철회했다. 다만 kick은 비동기 취소이며 지연된 AddPublisher가 잠시 Ready를 만들 수 있으므로, 설정값을 합산한 40초를 모든 생성 작업의 안전한 종료 상한으로 주장하지 않는다. [session 구현](https://github.com/bluenviron/mediamtx/blob/v1.21.3/internal/servers/webrtc/session.go#L503-L528)

주소가 없는 unknown POST의 자동 회수는 **미완료**다. 시간만으로 ENDED로 만들지 않으며 04-B/07에서 종료 증거·incarnation 귀속을 설계하고 검증한다. 이 제한을 남긴 상태로 정상 흐름과 안전한 권한 차단 인터페이스를 04-B2에 제공한다. 실제 PC 캡처·물리 두 기기·외부망·Google 자격·VAAPI/ABR·용량 검증도 이 리뷰로 완료되지 않는다.

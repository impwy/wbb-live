# 07-A1 미디어 종료 완료 계약 사전 검토

현재상태: 지정단독구현자가격리스파이크를진행중이다. 이문서는최종코드승인이나제품종료정확성의증거가아니다.

## 설계에서 확정한 경계

- 실제 MediaMTX 세션·publisher와 위임작업의 수명을 registry에 연결한다. 모의 registry만으로 완료를 판정하지 않는다.
- 고정 slot anchor는bootstrap설정이며매부트갱신하는별도저장소로확장하지않는다. 정상시작은기존이력검증과동일inode잠금뒤새epoch를영속화한다.
- 생성handler와위임session/작업만종료대상으로추적한다. close/queryhandler를자기완료조건에넣지않는다.
- 초기실험은기존제품스택을교체하지않고독립Linux자원에서종료장벽·두방송보존·재시작을검증한다.

## 원본 소스의 추가 수명 위험

고정upstream의 `session.go`는 `go s.readRemoteCandidates(...)`를 `errGroup` 밖에서시작한다. 이작업은 `s.ctx.Done()`을듣지만해당취소는 `runInner()`가반환한뒤이뤄진다. 따라서 `errGroup.Wait()` 반환만으로 이위임작업의종료를증명하지못한다. 단순히같은errgroup에추가하면취소순서에따라교착할수있다. 별도join/ref 또는명시적인취소순서보완과PATCH/ICE재시작중종료검증이필요하며지정구현자에게전달했다.

소스: [MediaMTX v1.21.3 세션](https://github.com/bluenviron/mediamtx/blob/v1.21.3/internal/servers/webrtc/session.go). 이는구현전반증이며수정완료로기록하지않는다.

## 독립 비동기 수명 검토

읽기전용서브에이전트의다음결과를조정자가원본에서재확인하고구현자에게전달했다. 아직수정후검증결과가아니다.

1. `internal/protocols/webrtc/inbound_track.go`의RTP/RTCP/PLI작업은별도goroutine이며close에join이없다. PLI는다음ticker의쓰기오류까지남고RTPcallback도진행중일수있다. `PeerConnection.Close`의done만으로이작업완료를주장하지않는다. 읽기를깨울GracefulClose보다먼저읽기join을대기하면교착할수있다.
2. `internal/core/path.go`의RemovePublisher는path context취소시즉시반환하지만path.run은그뒤stream정리를수행한다. path취소직후/정리직전장벽으로작업완료증거를검증해야한다.
3. session.Close를동기join으로바꾸면path이벤트루프의publisher교체가RemovePublisher응답을기다리는세션과교착할수있다. 취소요청과완료관찰을분리하고교체경합을검사한다.

Connected/Failed의단순상태관찰goroutine은미디어/생성/자원보유효과가없음을검토하여계약의관찰자범위를구분할수있다. RTP/RTCP쓰기,ICE재시작,경로/stream정리는실제효과가있으므로완료근거에포함한다.

## 초기 구현 중간 지적

`internal/wbbcompletion/registry.go` 초기후보는epoch를이력길이+1인숫자로만표현하고과거epoch완료도숫자범위로판정한다. ADR의무작위epoch_id와연속sequence분리계약을충족하지않는다. 새저장소에같은slot을명시bootstrap하면과거epoch값과충돌할수있고,내부Root는외부tuple에포함되지않는다. 무작위ID를영속화하고서명·조회·종료가정확한epoch ID를사용하도록수정요청했다. 최종patch검토전중간후보에대한지적이다.

초기session연결patch의 `sx.initialize()`는goroutine을먼저시작한뒤AttachSession으로참조를등록했다. 이미취소된parent context에서는DoneSession이선행하여참조계수와완료판정이틀릴수있으므로,ctx초기화→작업참조등록→goroutine시작순서로수정요청했다. 이미취소된context와동시close회귀가필요하다. 무작위epoch ID와sequence분리수정은코드상추가됐으며최종인증/조회연결및실제재시작검증은미완료다.

### 수정 후 중간 확인

무작위 epoch ID와 별도 sequence, 정확한 과거 ID 조회가 추가됐다. session.initialize에서 context 초기화 후 AttachSession을 등록하고 goroutine을 시작하도록 순서를 수정한 것도 확인했다. 기초 registry 검사 통과 보고를 받았으나 프로토콜 패키지의 `[no tests to run]` 결과는 컴파일 확인이며 실제 송출 종료 회귀 통과로 보지 않는다.

추가 독립 소스 검토에서 path.RemovePublisher의 취소 후 done 대기는 현재 RTSP/RTMP/SRT publisher.Close가 취소만 하므로 새 순환 대기를 만들지 않는 것으로 확인됐다. inbound reader join을 Pion GracefulClose 뒤에 둔 순서도 타당하다. 실제 RTSP path 종료, RTP callback 진행 중 close, PLI 대기 중 close 검사는 여전히 필요하다.

## 범위 일치 검토

초기 validateCompletionConf는 RTSP를 모두 거절하고 WHIP 발행·HLS 읽기만 허용했다. 이는 기존 WHIP→RTSP→FFmpeg→RTSP 출력→LL-HLS 구성과 두 방송의 변환 프로세스 보존 검증을 충족하지 못한다. 추적 대상 ingress의 계약 없는 발행을 막되, 내부 변환기의 RTSP 읽기·출력을 경로와 권한으로 제한해 지원하도록 수정 요청했다. 쉬운 직접 HLS 경로만 통과한 것으로 전체 스파이크 완료를 대신하지 않는다.

RTSP 범위 보완안은 source 읽기·720p 출력 발행을 HTTP 인증의 프로토콜/회차 경로/action으로 제한하고, 관리 API 조회·kick을 유지하면서 설정 변경을 차단하는 방식으로 검토했다. 실제 다른 회차·동작·프로토콜 우회 거절은 검증 전이다. lifecycle 응답의 contract_version 누락도 지적하여 명시 버전과 검사 항목이 추가됐다. 기초 HTTP/세션 검사와 실제 두 방송·부트 장애 검증은 구분한다.

장벽 초안은 작업 context 취소 시 자동 해제되어 취소 후에도 멈춘 실행이 남는 경합을 재현하지 못한다. 등록 이후 장벽에 작업 취소와 독립적인 실험 hold/release를 추가하도록 요청했다. 등록 전 close의 completed는 이후 Begin을 막는 표식이 근거이며, 이미 등록된 실행의 closing 유지와 구분한다. 인증 중 장벽과 실제 완료 응답 유실 검사도 남아 있다.

수정된 hold 장벽의 AbortBarriers는 abort 채널을 닫은 뒤 nil로 바꾸지만 gates는 남겼다. 종료 중 그 이후 진입한 WaitBarrier가 미해제 gate와 nil 채널을 기다려 멈출 수 있어 수정 요청했다. 닫힌 채널을 보존하거나 abort 상태에서 신규 Wait/Arm을 거절하고, abort 이후 신규 대기 및 동시 진입 회귀가 필요하다.

## 요청 취소와 실행 인계의 추가 경합

새 newSession 구현은 서버가 요청을 받은 뒤에도 HTTP context 취소로 반환한다. 서버가 IsClosing=false 확인 뒤 멈춘 동안 handler가 CloseOperation/DoneCreation을 실행하면 refs=0으로 completed가 될 수 있고, 이후 서버가 AttachSession을 수행하여 이미 완료된 작업에 실행이 추가된다. accept된 서버 작업의 별도 참조를 유지하거나 서버 응답까지 handler의 실행 소유권을 유지해야 한다. IsClosing 확인과 세션 등록 사이 장벽에서 요청 취소·close·상태 역행 여부를 검증하도록 전달했다.

## 중간 인증 경계 리뷰

별도 읽기 전용 리뷰에서 publish HMAC의 purpose/root/slot/epoch/operation/path/SDP 결합과 관리 API 설정 변경 차단에 새로운 확정 우회는 발견하지 못했다. RTSP 경로별 HTTP 인증 harness는 아직 구현·실행 전이므로 보호 완료로 보지 않는다.

실험 장벽의 arm/release/query 서명은 현재 epoch 대신 stage만 포함하여 같은 root/slot의 다음 실행에서 이전 서명을 재생할 수 있었다. 제품 종료 계약 우회와 구분되는 실험 결함이며, 현재 epoch와 stage를 각각 서명에 포함하고 과거 실행 서명을 거절하도록 보완 요청했다.

수정 후 newSession은 서버가 받은 요청의 결과를 받을 때까지 생성 소유권을 유지하도록 바뀌었고 관련 단위 회귀가 추가됐다. 장벽 서명에 현재 epoch를 결합하는 수정과 과거 서명 거절 검사도 추가됐다. 실제 자식 프로세스를 종료해 A→B→C epoch 이력과 잠금 해제를 확인하는 기초 검사가 작성됐으며, 이는 실제 Linux MediaMTX 부트·미디어 장애 검증과 별도다.

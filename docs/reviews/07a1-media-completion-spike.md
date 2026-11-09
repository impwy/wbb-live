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

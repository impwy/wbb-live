# 07-A: 미디어 생성·종료 완료의 근거

**Status:** needs-info
**Blocked by:** 04-B1 정상·권한·종료 Interface 로컬 검증(완료). 남은 것은 내부 종료증거 설계이며 사용자 답변을 기다리는 상태가 아니다.
**구현 담당:** 현재07-C 지정 단독 구현자의 후속 작업. 동시에 제품 편집하지 않는다.

## 발견한 결함

고정MediaMTX v1.21.3은 kick/WHIP DELETE에서 세션 map을 먼저 지우고 context 취소를 요청하며 생성 goroutine 완료를 기다리지 않는다. ToStream 성공 직후 지연된 goroutine은 삭제·목록부재 관측 뒤에도 SkipAuth AddPublisher를 호출하고 이후 취소를 읽어 정리할 수 있다. 이는 공식소스에 근거한 경합 반례이며 실제 장시간정지 장애 재현은 아직 없다.

현재 wbb-backend/internal/mediaworker/worker.go의cleanup은 ingress kick 및 known resource DELETE 후5초 부재 관측으로 해당 생성작업을closed로 기록한다. 이시간은 goroutine 지연 상한이 아니므로 known Location에도 동일한 완료증거 결함이 있다. unknown 작업만의 문제로 축소하지 않는다. 기존 정상실행에서 실제자원부재를 확인한 테스트 결과는 유효하지만 모든경합에서늦은재등장이불가능하다는증거는아니다.

근거: [kick](https://github.com/bluenviron/mediamtx/blob/v1.21.3/internal/servers/webrtc/server.go#L467), [Close](https://github.com/bluenviron/mediamtx/blob/v1.21.3/internal/servers/webrtc/session.go#L299), [늦은 publisher 등록](https://github.com/bluenviron/mediamtx/blob/v1.21.3/internal/servers/webrtc/session.go#L498).

## 설계 후보와 제한

- callback UUID를 허용 전에 영속 기록하고 stop과 DB 잠금 순서로 직렬화하면 승인된 생성세션의 식별 공백을 줄일 수 있다. UUID는 Location secret과 달라 삭제URL을 복원하지 못한다. 기록만으로 종료증거가 되지는 않는다.
- 강제삭제가 한 번도 없었던 동일실행의 정확한UUID 자연소멸은 후보가 될 수 있다. 정상 session.run은 runInner/errGroup.Wait와publisher 제거 후closeSession으로 map에서 제거한다. callback시점실행귀속·관리조회연속성·단일인스턴스·설정변경통제가 필요하다. 조회전후started동일만으로callback실행과같다고증명하지못한다.
- 모든승인이DB기록후허용되고stop과직렬화되면 승인0건은향후publish불가의근거후보다. 아직인증대기중인세션객체까지종료됐다는뜻은아니다.
- API는 AttachPublish commit 후클라이언트에answer를쓴다. unknown은일반제품경로에서answer를받지못해자연handshake종료가예상되지만,timeout경과만으로완료를단정하지않는다.
- 별도실행종료증거는대안이다. Docker socket/전역재시작/다른방송중단을필요조건으로추가하지않는다. 확정설계전기존unknown STOPPING 안전성을약화하지않는다.

## 완료 기준

known/unknown 모두정확한과거생성작업의완료를증명한뒤에만closed/ENDED를확정한다. 응답유실/API재시작,취소직후늦은생성,이전응답과새회차경합,프로세스교체/설정변경을검증한다. 별도정상방송의영상/음성/변환을보존한다. 권한차단은즉시유지하며관측된자원정리와향후재생성불가능을구분한다. 안전한상태회수·운영복구문서와실제장애증거를조정자가독립검증하기전완료처리하지않는다.

## 다음 실행 범위

제품 교체 전 `07a1-media-completion-spike.md`의 격리 Linux 스파이크로 ADR0001 계약을 증명한다.07-C 동결 후 지정 단독 구현자에게 인계하며, 스파이크 완료와 제품 통합 완료를 구분한다.

제품 통합 초안은 `07a2-media-completion-integration.md`에 분리했다. DELETE 응답 기반 CloseResource와 stop/dispatch 직렬화까지 포함하며, 07-A1 완료·ADR 채택 전 구현하지 않는다.

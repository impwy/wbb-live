# ADR 0001: 생성 작업 단위 미디어 종료 완료 계약

상태: 채택(07-A1 격리 계약, 2026-11-11). 고정 MediaMTX 패치·Linux 실제 미디어/장애 검사와 두 축 리뷰를 확인했다. 제품 API·worker 통합은 07-A2에서 별도 검증하며 기존 제품 MediaMTX 이미지는 아직 변경하지 않았다.

## 문제와 선택 후보

07-A의 known/unknown 생성 경합은 kick/DELETE 성공과 짧은 목록 부재로 완료를 증명할 수 없다. 프로세스 supervisor만으로는 특정 작업 종료를 확인할 수 없고, 매번 MediaMTX 재시작을 요구하면 다른 방송을 중단한다. callback UUID의 자연 소멸은 강제삭제 이력이 없고 동일 실행임이 입증돼야 하므로 일반 종료의 주해법으로 부족하다.

고정 MediaMTX v1.21.3에 제한된 작업 등록·종료 완료 계약을 추가한다. upstream 출처·라이선스·정확한 commit과 patch를 `experiments/media-completion/`에 보존하고 전용 이미지로 빌드한다. 원본 전체를 출처 없이 제품 코드로 복사하지 않는다. 공식 버전 교체 때 patch 적용과 종료 회귀를 다시 검증한다.

## 채택한 인터페이스

- 관리망의 lifecycle 조회는 `root`, `slot`, 무작위 `epoch`, 연속 `epoch_sequence`, `contract_version`을 반환한다.
- WBB용 WHIP POST는 operation_id, expected_epoch와 내부 인증을 전달한다. operation은 기존 DB의 media_create_operations ID다. 경로/자격/operation 귀속을 검증하며 외부 클라이언트가 식별자만 보내 작업을 제어할 수 없게 한다. 내부 자격은 epoch·operation·요청 대상에 결합하고 기존 외부 소유자 인가는 유지한다.
- 세션 할당 전에 epoch 일치와 operation의 open 여부를 검사하고 생성 작업을 등록한다. operation close와 이 등록은 같은 직렬화 지점을 사용한다. 새 WBB 전용 경로에서 이 계약 없는 발행은 거절한다.
- operation close는 먼저 해당 작업을 닫고 등록된 생성/세션을 취소한다. 아직 세션이 없어도 종료 표식을 남겨 뒤늦은 POST를 거절한다. 다른 operation은 취소하지 않는다.
- 작업 조회는 closing/completed/unknown_epoch를 구분한다. completed는 등록된 모든 생성 실행, 세션 goroutine, publisher 제거가 끝났으며 같은 epoch에서 추가 등록도 불가능함을 뜻한다.404나목록부재는completed가아니다.
- close는 멱등이고 응답 유실 후 같은operation/epoch로 재조회한다. 취소 요청을 완료로 취급하지 않는다. 서버 단일 이벤트 루프가 종료 goroutine을 기다리며 교착하지 않도록 완료 통지는 별도로 처리한다.
- 동일 실행에서 닫힌 작업 표식을 시간만으로 버리지 않는다. 저장/용량 제한은 fail-closed여야 하며 오래된 POST가 다시 등록될 수 있는GC는 금지한다. 장기 저장 정책을 구현 전 명시한다.

## 재시작 증거

로컬 단일 노드 배치에서는 MediaMTX 프로세스가 고정 볼륨의 동일 inode 배타 잠금을 실제 종료까지 보유한다. 새 실행은 잠금을 획득한 뒤 이전 epoch 종료와 새 epoch를 영속 기록하고, 그 기록이 안전하게 저장된 뒤에만 요청을 받는다. 단순started변경이근거가아니다. 이전epoch요청은새epoch에서거절하고종료된epoch의작업은재생성되지않는다.

잠금 파일 교체/조기해제 또는 검출된 이력 불일치에서는 요청 수락을 막고 종료를 증명하지 않는다. 복제·롤백 볼륨, 서로 다른 저장소를 같은 slot으로 사용, 다중 노드의 강제삭제/분할뇌는 지원하지 않는다. checksum과 canonical 형식 검사는 우발적 손상을 검출하지만 악의적인 이력 재작성이나 과거 볼륨 복원을 검출한다고 주장하지 않는다. k3s 배치 계약은 동일 노드·동일 볼륨의 일관성 확인 후 설정하며 무검증 failover를 허용하지 않는다. 이 방식은 운영 방송을 끊기 위한 재시작을 요구하지 않는다.

## WBB 적용과 검증

STOPPING·송출권한폐기는DB에서먼저commit한다. 생성전epoch/operation귀속을저장하며미등록구형작업에나중에관측한epoch를소급대입하지않는다. 정확한operation completed 또는검증된이전epoch종료와, FFmpeg종료·새자원조회가모두충족되어야ENDED로전이한다. 늦은응답이새회차에영향을주지않아야한다.

검증지점은등록직전/인증중/AddPublisher직전/완료응답직전이다. 각지점에서지연·API종료·응답유실을주입하고known/unknown재시도,MTX재시작,중복close,다른정상방송의PID/ingress/영상음성보존을확인한다. 단기정상검사와경합장애검사를구분한다. 기존8090실험과다른사이트를교체하지않는다.

근거: [MediaMTX v1.21.3](https://github.com/bluenviron/mediamtx/releases/tag/v1.21.3), [현재kick/delete](https://github.com/bluenviron/mediamtx/blob/v1.21.3/internal/servers/webrtc/server.go#L423), [세션완료와publisher등록](https://github.com/bluenviron/mediamtx/blob/v1.21.3/internal/servers/webrtc/session.go#L303). 기존진단과반례는07a-media-completion-evidence.md를따른다.


## 독립 반증 검토 반영 조건

단일 Linux 호스트·동일 로컬볼륨 배치에서 조건부 실행 가능하다는 검토를 받았다. 아래 조건을 07-A1 격리 스파이크에서 검사했으며 아직 제품에는 적용하지 않았다.

1. 모든 dispatched 작업은 upstream4xx도종료증거로취급하지않는다. 현재CloseFailedPublish의4xx즉시closed경로를최종적용시대체한다. completed 또는검증된이전epoch종료만최종근거다.
2. `(slot,epoch,operation)` 등록부터세션할당/인증/HTTP취소/중간실패/완료까지단일소유권을보존한다. 같은operation의다른경로·본문재사용은거절한다. 완료표식은실제goroutine/publisher제거이후다.
3. registry와잠금은WebRTC객체가아닌최상위프로세스수명에귀속한다. 초기지원에서는해당동적설정reload와외부자식/hook을금지한다. 잠금FD를정상종료도중먼저해제하지않는다.
4. A→B→C에서중간B를WBB가관측하지못해도A의종료근거를확인할수있도록안정slot ID와연속epoch이력/순번을보존한다. 잠금획득→새기록작성→파일fsync→원자적교체→디렉터리fsync→요청수락순서이며잠금inode자체는교체하지않는다. 손상/빈기존볼륨/slot불일치는자동새slot초기화하지않는다.
5. 등록시완료표식공간까지보장하고상한에도기존작업close/query가작동해야한다. epoch내표식은삭제하지않는다. 미도착작업close시공간부족을조용히완료처리하지않는다. 구현후보는포화epoch의신규등록을영구봉쇄한뒤미등록작업은앞으로도생성불가임을명시적으로증명하는것이다. 봉쇄는같은epoch에서풀지않고최상위registry직렬화로등록중요청을포함해야한다. 이계약증명전에는포화미등록close를미완료로유지한다. 장기무중단무제한시작을보장하지않으며용량·유지보수조건을명시한다.
6. close/query는별도관리권한으로인증하며현재송출허용상태에의존하지않는다. 내부자격은slot/epoch/operation/정확경로/용도에결합한다. DB귀속commit후전송하고늦은응답은귀속을바꾸지못한다.

초기지원제외: NFS잠금가정,복제/복원볼륨,자동다른노드이동,비협조프로세스,legacy작업epoch소급귀속. 지원배치에서각부트/등록/종료경계의장애주입과다른방송보존증거를확보한후ADR확정·제품통합을판정한다.

## 초기화와 증거의 신뢰 경계

slot ID는 명시적 배치 설정에 고정한다. 새 저장소 초기화는 별도 bootstrap 동작으로 제한하고 일반 서버 시작에서 비어 있는 저장소를 자동 초기화하지 않는다. 일반 시작은 설정의 slot anchor와 저장된 slot/epoch 이력의 일치를 확인한다. 이 구분 없이 빈 볼륨에서 새 epoch를 만들면 과거 실행 종료를 증명할 수 없다. 잠금 파일의 자동 재생성·교체로 이전 프로세스와 별개 잠금을 획득하는 것도 허용하지 않는다.

실험에서 운영자가 명시적으로 bootstrap한 새 저장소의 이력은 그 저장소 안에서만 유효하다. legacy 작업, 복제·복원·롤백 저장소, 다른 노드의 실행 종료를 소급 증명하지 않는다.

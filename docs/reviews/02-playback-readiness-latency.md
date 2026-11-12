# 재생 준비 상태·지연 리뷰

기준6cb0b2a 이후 변경을 고정 복사해 두 축으로 독립 검토했다. 구현은 지정 대체 Codex가 수정하고 조정자가 검증했다.

## Standards

잔여 확정 결함0건. 중간 검토에서 네트워크 오류를 거친 종료 시 플레이어 정리가 누락되는 문제와 BFCache 복귀 후 폴링이 멈추는 문제를 재현했다. 수정 후 활성 플레이어 기준 정리, pageshow 재검사, epoch 검사에 의한 이전 요청의 중복 타이머 방지를 격리 VM에서 확인했다.

## Spec

잔여 확정 결함0건. 같은2건을 독립 재현했고 수정 후 network→404→ready에서 이전 플레이어 제거와 BFCache 복원 시 checking/disabled·재검사 예약을 확인했다. 실제 미디어 검사는 [검증 기록](../verification/02-playback-readiness-latency.md)을 따른다.

지연 측정은 합성 canvas→수신 readback과 player live edge 지표를 구분한다. 사용자의 큰 실제 캡처 지연은 미재현이며 서버 튜닝으로 해결됐다고 주장하지 않는다.

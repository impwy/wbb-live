# 04-B2 Flutter 송출·시청 리뷰

기준: 서버107cdd8에서 work/flutter-live의 최종 작업 트리와 신규 파일까지. 지정 대체 Codex가 구현했고 조정자는 독립 검사, 두 별도 리뷰 에이전트는 읽기 전용 명세·표준 검토를 수행했다. 최종 검증 동안 제품 파일 변경은 없었다.

## Standards

잔여 확정 결함0건. 오류 안내의 방송 식별자가 자원 정리와 함께 사라지던 문제를 수정했고 제품 provider 구독으로 화면에 반영한다. JSON·SDP transport의 인증/재시도 중복은 선택적 개선 판단1건이며 이번 범위의 필수 리팩터가 아니다.

## Spec

잔여 확정 제품 결함0건. 종료 실패 안내, 새 회차 발견 시 이전 플레이어 즉시 폐기, 사용자 오디오 재개를 수정하고 직접 widget 회귀를 추가했다. 실제 기기·외부망·unknown POST 회수는 아래 별도 관문으로 유지한다.

## 수정·검증한 주요 경계

- 이전401/refresh/logout/restore 응답이 새 로그인 상태를 덮지 않도록 세대를 검사한다.
- 중복 시작, 시작 중 취소, 늦은 prepare/WHIP201은 정확한 시도·회차·resource만 정리한다.
- 늦게 허용된 display/mic 트랙은 Map 등록 여부와 무관하게 해당 시도에서 종료한다.
- 화면 이탈·로그아웃 시작에 로컬 정리를 수행하고 서버 종료 대기로 로그아웃이 무기한 막히지 않는다.
- 종료 실패 문구, 회차 변경 중 재생 비활성화, suspended 오디오의 재개 버튼을 사용자 동작으로 확인한다.
- 사용자 pause를 poll·라이브 이동이 해제하지 않는다. native 분기는 대역검사이며 실제 Safari 검증과 구분한다.
- 브라우저 검증은 정확한 API ended, 실제 ingress/RTSP/FFmpeg 부재와 이전HLS 거절을 확인한다. ‘종료 처리 중’ 문자열을 종료 성공으로 취급하지 않는다.

## H264 진단과 설정

2초 track 수집 설정에서 제품bridge의 H264 전송은3304ms까지0→3555ms 표본에서 증가했고 MTX는Opus만수집했다. 전용6초 대조에서는3290ms까지0→3542ms 증가, source는H264+Opus였다. Flutter overlay에서만6초를 사용한다. 예상 트랙이 모두 도착하면 즉시 진행하며 WHIP answer는 수집 전에 반환된다. [고정 MediaMTX 설정](https://github.com/bluenviron/mediamtx/blob/v1.21.3/mediamtx.yml#L430-L431), [처리 순서](https://github.com/bluenviron/mediamtx/blob/v1.21.3/internal/servers/webrtc/session.go#L452-L481).

## 최종 근거와 제한

조정자 analyze0·Flutter53개·JS3개·릴리스 빌드 및 실제 제품 UI의 두 Chrome 독립 재실행을 확인했다. 자세한 수치와 검사 범위는 [검증 기록](../verification/04b2-flutter-live.md)을 따른다. 자동 캡처는 합성이며 실제 OS공유창·물리 두음원·iPhone/외부망·실제 native HLS·브라우저 정책에 따른 오디오 재개 인수를 대체하지 않는다. 주소를 얻지 못한 WHIP POST 자동회수는04-B/07의 미완료 관문이다.

최종 주요 SHA-256:

- publisher: fa09024a261662a738b0800d403609adf63f9fa06cdd32b05fc78f8a29d10492
- detail: cc1adafb918fea2d4cf305fa31b72eff0f6d93147fe427829fddf8fe76f81c03
- JS: 43647d423bc6beedb54112ab2a6ebfe7daeb9d6a33bdb576f2f1b1fac1498781
- verify-flutter: 50f3880b3f97855f2018928debc29b05103b403c7cc15477ac0d907ae19f5017

표준 축 확정0건(선택 개선1건), 명세 축 확정0건.

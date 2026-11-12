# 04-B2 Flutter 로컬 미디어 검증

상태: 로컬 PC 통합 구현·독립 검증 완료. 기준 서버107cdd8, 작업 브랜치work/flutter-live. 이 문서는 실제 기기·외부망 인수나 전체 필수 기능 완료를 뜻하지 않는다.

## 조정자 독립 검사

- Flutter analyze: 지적0, exit0.
- Flutter test: 전체53개 통과, exit0.
- 별도 출력 폴더의 `flutter build web --release --no-web-resources-cdn`: 성공, exit0. CupertinoIcons 폰트 계열 안내가 출력됐으며 실제 사용 화면의 아이콘 검증과 구분한다.
- `live_detail_test.dart`4개: 시작 중 취소, 종료 실패 안내, 새 회차 playback 대기·409 중 이전player 폐기, 사용자 오디오 재개 버튼과running표시.
- 별도 Node VM: 늦은 display/mic 획득 후 종료, 무기한 audio.resume의 유한 대기, 동기 재개 호출, 종료 후 모든 트랙·AudioContext 정리. 실제 권한창 검사가 아니다.
- 추가 송출 상태6개 독립 통과: 중복begin의 단일 캡처, 중지 뒤 늦은WHIP201의 과거resource만 삭제, 새 시도 유지, 로그아웃HTTP 전 로컬정리hook 호출 및 기존 취소·늦은응답 경계. 로그아웃 검사는 캡처 권한 대기 중인 시도이며 실제 방송 중 로그아웃 미디어 수신 검사를 대신하지 않는다.
- Flutter 실행 문서의 Compose 조합을 config로 확인했다. 전용project와 Caddy 정적파일·MediaMTX 설정 mount가 의도대로 해석됐으며 이 확인 명령으로 서비스를 변경하지 않았다.

## 구현자의 실제 제품 UI 검사

별도 Chrome 프로세스2개와 전용 HTTPS 스택에서 실제 Flutter 가입·로그인·방송 등록·송출·익명 시청·종료를 실행했다. 캡처만 검증기의 addInitScript에서 합성 영상과440/660Hz 음원으로 대체했으며 제품에 합성 송출 기능을 추가하지 않았다.

첫 성공 결과: 익명 시청9프레임, 두 음원-52.04/-52.37dB, 주변 대역-113.91dB. 정확한 서버ended와 화면 종료, 이전 회차의 실제 ingress/RTSP/FFmpeg 부재, 브라우저 트랙·PeerConnection·AudioContext 종료를 검사했다. 조정자는 스크립트와 결과 JSON을 확인했으며 같은 전체 UI 흐름의 독립 재실행도 아래 결과로 통과했다.

## 조정자 독립 제품 UI 재실행

동결된 최종 제품에서 별도 Chrome2개로 가입·로그인·방송등록·송출·익명시청·pause유지·종료를 재실행했다(exit0). H264 outbound3프레임/19패킷과Opus송출, MTX H264+Opus2트랙, 익명디코딩7프레임, 두음원-52.07/-52.37dB·주변대역-111.05dB를 확인했다. poll과라이브이동후pause가유지됐다. 종료후playback409/HLS403, 정확한ended와실제미디어자원부재·브라우저트랙/PC/AudioContext정리를확인했다.

검증전후제품·실험파일해시는동일했다. 종료후전용DB ended25개·활성0개,기존8090 HTTP200을확인했다. 마지막전체analyze0·Flutter53개도독립통과했다.

## H264 시작 지연 진단

기본2초 track gather에서는 영상전송이3304ms까지0이고3555ms 표본에서처음증가해 MTX가Opus만수집했다. 동일 제품bridge의6초대조에서는3290ms까지0→3542ms증가, source H264+Opus2트랙을 확인했다. 해당 값은 표본 시각이며 정확한 최초 패킷 시각으로 표시하지 않는다.

Flutter 전용MediaMTX설정은 `webrtcTrackGatherTimeout: 6s`다. 모든 예상 트랙이 먼저 도착하면 즉시 진행하며, 영상이 끝내 오지 않는 경우를 정상LIVE로 보장하지 않는다. 원래04-B1 설정과 기존8090 서비스는 보존한다.

## 남은 검증

- 두 축 최종 재검토의 잔여 확정 결함은 각각0건이다. 실제 방송 중 물리기기 로그아웃/브라우저 종료 인수는 아래 실기기 검사에서 확인한다.
- 실제PC공유창, 실제 마이크와 공유소리, 물리 두 기기, iPhone Chrome, 외부망, Google 실제 자격은 별도 인수다.
- unknown WHIP POST 자동회수는04-B/07에 미완료로 남는다.

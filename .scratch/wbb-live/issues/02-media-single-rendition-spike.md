# 02: 실제 브라우저에서 단일 품질 라이브 검증

**What to build:** 제한된 검증 화면에서 실제 PC/휴대폰이 MediaMTX WHIP로 보내고 별도 기기가 FFmpeg 출력 LL-HLS를 재생하는 최소 흐름을 증명한다. 구현 전제의 실패를 빨리 드러내는 실행 가능한 검증이다.

**Blocked by:** None (can start immediately). 같은 pi 세션에서는01과 동시 편집하지 않는다.

**Status:** in-progress

**계약:** 실제 서비스 인증 통합은04에서 한다. 검증 endpoint를 공개 무인증 송출 서비스로 배포하지 않는다. 먼저 단일 rendition, 고정 버전, 실측 codec/manifest를 사용한다.

- [ ] PC 화면/탭+공유 소리+마이크가 Web Audio 단일 트랙으로 전달되고 별도 기기에서 두 음원을 구분해 듣는다.
- [ ] 모바일 카메라·마이크가 별도 기기에서 보이고 들린다. 기기가 없으면 미검증으로 남긴다.
- [ ] MediaMTX WHIP POST/Location/PATCH/DELETE와 명시적 ingress 종료 API를 확인한다.
- [ ] 실제 입력 codec→FFmpeg VAAPI1080/720/480 중 한 품질→H.264/AAC LL-HLS가 재생된다. 인코딩만 성공한 합성 테스트와 구분한다.
- [ ] 권한 거절·공유 중지·부분 실패가 track/AudioContext/PeerConnection/변환 프로세스를 정리한다.
- [ ] 외부망에서 ICE 경로를 확인하고 TURN 필요 여부·필요 포트를 비밀 없이 기록한다.
- [ ] 브라우저/OS/기기/네트워크/날짜, 영상·음성·종료 결과와 명령을 기록한다. 모의 미디어 결과를 실제 결과로 표시하지 않는다.

**검증:** PC-01~04, MOB-01~03의 기술 관문. 보안 통합이 없으므로 제품 인수 완료는 아니다. 미디어 전제 실패 시 원인·대안을 Codex에 전달한다.

**결과:** 2026-11-07 패키지 복구 후 지정 pi GLM에서 로컬 실험을 구현했다. WHIP/ICE·H.264/AAC·LL-HLS part를 확인했고 검증은50통과·1건 건너뜀·재생2실패다. 기본 Chromium의 H.264 미지원과 Chrome의 native HLS 선택 실패를 분리했다. Codex 진단용 MSE 강제 경로에서는 재생되지만 구현은 아직 수정되지 않았다. GLM이5시간 사용 한도429/1308에 도달해 추가 수정이 막혔다. 제공자가 안내한 재개 시각은2026-11-08 01:56:19(시간대 미표기)다.

**재개 순서(구현은 기존 pi GLM):**

1. `docs/reviews/02-media-spike.md`와 `docs/verification/02-media-spike.md`를 읽는다. Chrome 코덱 사전 검사와 hls.js/MSE 선택·native 실패 fallback·오류 안내를 수정하고 실제 시간/프레임 증가를 재검증한다.
2. 화면 획득 즉시 ended를 처리한다. 마이크 대기 중 공유 중지 후 허용 회귀, 합성 초기화 부분 실패 정리, PC 두 음원 clipping 방지 gain을 구현한다.
3. `verify.sh`의 redirect·master→media playlist 탐색을 고친다. FFmpeg 원래 PID·출력 연결·새 part 생성의 종료를 확인한다. 검증 불가를 PASS로 처리하지 않는다.
4. Codex가 재검토·검증한 뒤 체크포인트를 main에 통합한다. 실제 두 기기·외부망·VAAPI·Safari·ABR는 계속 별도 관문이다. 검증용 HTML/JS는 제품 Flutter를 대체하지 않는다.

현재 스택은 MediaMTX1.21.3-ffmpeg, CPU libx264/AAC, loopback ICE18389 및 로컬 hls.js1.6.7이다. 스파이크 코드·문서 체크포인트는 별도 작업 브랜치에 보존하며 자동 전체 검사 통과나 티켓02 완료로 표시하지 않는다.

# 02: 실제 브라우저에서 단일 품질 라이브 검증

**What to build:** 제한된 검증 화면에서 실제 PC/휴대폰이 MediaMTX WHIP로 보내고 별도 기기가 FFmpeg 출력 LL-HLS를 재생하는 최소 흐름을 증명한다. 구현 전제의 실패를 빨리 드러내는 실행 가능한 검증이다.

**Blocked by:** None (can start immediately). 같은 pi 세션에서는01과 동시 편집하지 않는다.

**Status:** in-progress

**계약:** 실제 서비스 인증 통합은04에서 한다. 검증 endpoint를 공개 무인증 송출 서비스로 배포하지 않는다. 먼저 단일 rendition, 고정 버전, 실측 codec/manifest를 사용한다.

- [ ] PC 화면/탭+공유 소리+마이크가 Web Audio 단일 트랙으로 전달되고 별도 기기에서 두 음원을 구분해 듣는다.
- [ ] 모바일 카메라·마이크가 별도 기기에서 보이고 들린다. 기기가 없으면 미검증으로 남긴다.
- [x] MediaMTX WHIP POST/Location/PATCH/DELETE와 명시적 ingress 종료 API를 loopback 합성 환경에서 확인했다. 외부망·제품 인증 통합은 별도다.
- [ ] 실제 입력 codec→FFmpeg VAAPI1080/720/480 중 한 품질→H.264/AAC LL-HLS가 재생된다. 인코딩만 성공한 합성 테스트와 구분한다.
- [ ] 권한 거절·공유 중지·부분 실패가 track/AudioContext/PeerConnection/변환 프로세스를 정리한다.
- [ ] 외부망에서 ICE 경로를 확인하고 TURN 필요 여부·필요 포트를 비밀 없이 기록한다.
- [ ] 브라우저/OS/기기/네트워크/날짜, 영상·음성·종료 결과와 명령을 기록한다. 모의 미디어 결과를 실제 결과로 표시하지 않는다.

**검증:** PC-01~04, MOB-01~03의 기술 관문. 보안 통합이 없으므로 제품 인수 완료는 아니다. 미디어 전제 실패 시 원인·대안을 Codex에 전달한다.

**결과:** 2026-11-08 02:57 KST 재개 요청 후 지정 pi GLM의 실제 작업 시작을 확인했다. 이전 01:57 요청의429/1308과 달리 구현·수정이 수행됐다. pi와 Codex의 독립 전체 실행은 각각83통과·건너뜀0·실패0이다. Chrome H.264/AAC preflight, MSE 우선 및 native 실패 처리, 공유 중지 경합, 합성·PC destination 트랙 정리, 혼합 gain, 원래 FFmpeg 종료와6초 관측을 확인했다. 별도 verify.sh는 H.2641280×720/AAC를 검증했고, 관리 API 강제 종료200·source/output 비활성·세션0·동일 세션 재종료404를 관측했다. 합성/모의 결과이며 실제 기기 관문은 완료하지 않았다.

**다음 순서(구현은 기존 pi GLM):**

1. `docs/reviews/02-media-spike.md`와 `docs/verification/02-media-spike.md`의 로컬 수정·독립 검증 결과를 기준으로 진행한다. 로컬 체크포인트 통합과 티켓02 전체 완료를 구분한다.
2. 실제 PC의 화면+탭/시스템 소리+마이크, 휴대폰 카메라+마이크를 별도 물리 기기에서 확인하고 시작·시청·종료·권한 거절 결과를 남긴다. 실제 캡처·청취는 사용자와 함께 진행해야 한다.
3. Labtop VAAPI 단일 품질, 안전한 HTTPS 접근, 외부망 ICE/NAT/TURN과 Safari 재생을 확인한다. 인증 없는 현재 loopback 실험을 그대로 공개하지 않는다.
4. 실제02 관문 충족 전03 ABR 및04 제품 송출 통합을 완료로 처리하지 않는다. HTML/JS 실험은 Flutter 제품 UI를 대체하지 않는다.

현재 스택은 MediaMTX1.21.3-ffmpeg, CPU libx264/AAC, loopback ICE18389 및 로컬 hls.js1.6.7이다. 로컬 합성 검사 성공을 실제 두 기기·VAAPI·외부망 성공으로 표시하지 않는다.

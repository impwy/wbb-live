# 02: 실제 브라우저에서 단일 품질 라이브 검증

**What to build:** 제한된 검증 화면에서 실제 PC/휴대폰이 MediaMTX WHIP로 보내고 별도 기기가 FFmpeg 출력 LL-HLS를 재생하는 최소 흐름을 증명한다. 구현 전제의 실패를 빨리 드러내는 실행 가능한 검증이다.

**Blocked by:** None (can start immediately). 같은 pi 세션에서는01과 동시 편집하지 않는다.

**Status:** ready-for-agent

**계약:** 실제 서비스 인증 통합은04에서 한다. 검증 endpoint를 공개 무인증 송출 서비스로 배포하지 않는다. 먼저 단일 rendition, 고정 버전, 실측 codec/manifest를 사용한다.

- [ ] PC 화면/탭+공유 소리+마이크가 Web Audio 단일 트랙으로 전달되고 별도 기기에서 두 음원을 구분해 듣는다.
- [ ] 모바일 카메라·마이크가 별도 기기에서 보이고 들린다. 기기가 없으면 미검증으로 남긴다.
- [ ] MediaMTX WHIP POST/Location/PATCH/DELETE와 명시적 ingress 종료 API를 확인한다.
- [ ] 실제 입력 codec→FFmpeg VAAPI1080/720/480 중 한 품질→H.264/AAC LL-HLS가 재생된다. 인코딩만 성공한 합성 테스트와 구분한다.
- [ ] 권한 거절·공유 중지·부분 실패가 track/AudioContext/PeerConnection/변환 프로세스를 정리한다.
- [ ] 외부망에서 ICE 경로를 확인하고 TURN 필요 여부·필요 포트를 비밀 없이 기록한다.
- [ ] 브라우저/OS/기기/네트워크/날짜, 영상·음성·종료 결과와 명령을 기록한다. 모의 미디어 결과를 실제 결과로 표시하지 않는다.

**검증:** PC-01~04, MOB-01~03의 기술 관문. 보안 통합이 없으므로 제품 인수 완료는 아니다. 미디어 전제 실패 시 원인·대안을 Codex에 전달한다.

**결과:** 아직 미실행.

# 02 단일 품질 미디어 검증

검증일: 2026-11-07. 구현: 지정 pi GLM. 설계·리뷰·검증: Codex와 리뷰 에이전트.

## 환경과 판정 범위

macOS arm64의 로컬 Docker 프로젝트 `wbb-media-spike`, MediaMTX `1.21.3-ffmpeg`, CPU FFmpeg, 로컬 번들 hls.js와 합성 canvas·oscillator를 사용한다. 모든 공개 포트는 loopback에 묶는다. 검증 HTML/JS는 Flutter 제품 UI가 아니다.

경로: 브라우저 합성 영상·음성 → WebRTC/WHIP → MediaMTX source → FFmpeg H.264/AAC → MediaMTX 단일 품질 LL-HLS → 브라우저 재생.

## 검사 기준

- WHIP POST201, Location/ETag, 실제 ICE 연결, PATCH204, DELETE 성공.
- 송신 bytes/frames와 수신 재생 시간/디코딩 프레임이 각각 증가.
- 실제 출력 코덱 H.264/AAC, 양수 치수, 너비1280·높이720 이하, 원본보다 확대하지 않음.
- master가 가리키는 media playlist의 LL-HLS parts와 blocking reload 지원, 실제 part 다운로드 및 새 part 진행.
- 종료 시 로컬 트랙·PeerConnection·AudioContext와 서버 ingress·해당 변환 연결 정리. 재시작과 늦은 비동기 응답의 격리.
- 화면·카메라 권한의 실패 및 지연은 자동화에서 합성 입력으로 모의한다. 실제 장치 권한 UX를 검증했다고 표시하지 않는다.

## 결과

`verify-synthetic.mjs` 결과는 **50통과·1건 건너뜀·2실패(총53)**다. pi 실행과 Codex의 Chrome 실행에서도 현재 자동 재생 경로는 실패했다. 건너뛴 항목은 브라우저 오디오 analyser이며 성공으로 합산하지 않는다.

| 검사 | 관측 |
| --- | --- |
| WHIP·ICE | POST201, Location/ETag, PATCH204, ICE connected, 실제 송신 bytes/frames 증가 |
| LL-HLS | media playlist parts, 실제 part GET200, part 진행, 요청 msn/part가 포함된 blocking reload 응답200 |
| 출력 | ffprobe H.264/AAC,1280×720; AAC를 PCM으로 복호화한576000바이트에서 비영(非零) peak 확인 |
| 종료·재시작 | source 비준비·WebRTC 세션0, 관찰한 track ended/PC closed/AudioContext closed; 재시작·늦은POST·깨진SDP·모의ended·늦은capture 회귀 통과 |
| 실제 자동 플레이어 | 재생시간0→0, 프레임0→0: 실패 |
| 독립 필터 검사 | 세로1080×1920→406×720,640×360→640×360,1280×1024→900×720; 세 경우 FFmpeg exit0 |
| 외부 바인딩 거절 | Go helper가 wildcard·비loopback 세 주소를 모두 거절 |
| 의존성 | `npm audit` 보고0건(Playwright npm 의존성 범위; vendor 및 컨테이너 전체 보안 감사 아님) |

로컬 PC를 먼저 닫으면 MediaMTX가 연결을 먼저 회수해 DELETE404가 될 수 있다. 검사는 해당 결과와 실제 서버 세션0을 함께 확인한다. 일반 DELETE 실패를 모두 성공으로 처리하지 않는다.

기본 Playwright Chromium141은 H.264 디코딩 미지원이다. Chrome156은 H.264/AAC 지원이 있지만 native HLS 우선 선택에서 NotSupportedError가 발생했다. Codex가 코드 파일을 바꾸지 않고 브라우저의 HLS 지원 응답만 진단용으로 덮어써 hls.js/MSE를 강제했을 때1280×720에서3초 간격으로 재생 시간13.501→16.404초, 디코딩 프레임0→85 증가를 확인했다. 이 결과는 원인 분리이며 현재 구현의 재생 검사를 통과시킨 것이 아니다. Safari native HLS는 별도 검증 대상이다.

GLM 제공자가5시간 사용 한도429/1308을 반환하여 플레이어 선택·검증기와 [남은 리뷰 사항](../reviews/02-media-spike.md) 수정은 보류 중이다. 안내된 재개 시각은 `2026-11-08 01:56:19`이며 제공자 응답에 시간대는 명시되지 않았다.

FFmpeg 원래 프로세스 종료와 출력 path 소멸·종료 후 새 part 생성 중단은 아직 검사 완료로 주장하지 않는다. 서버 PCM 비영 신호는 실제 스피커 청취 또는 PC 두 음원 혼합의 검증이 아니다.

## 아직 검증하지 않은 것

실제 PC 화면·공유 소리·마이크 혼합과 청취, 휴대폰 카메라·마이크, 다른 물리 기기 시청, 외부망 ICE/NAT/TURN, Safari HTTPS LL-HLS, Labtop VAAPI, 다중 품질 ABR, 장시간·부하 용량, 제품 인증 및 Flutter 통합, 운영 배포는 미검증이다. 티켓02 전체와 필수 라이브 기능은 완료가 아니다.

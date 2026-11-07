# 02 단일 품질 미디어 검증

검증일: 2026-11-08. 구현: 지정 pi GLM. 설계·리뷰·독립 실행: Codex와 리뷰 에이전트.

## 결과와 범위

**pi와 Codex의 별도 실행에서 각각83항목 통과, 건너뜀0, 실패0, exit0을 확인했다.** 로컬 합성·모의 검증의 결과이며 티켓02 전체 또는 제품 라이브 기능의 인수 완료는 아니다.

환경: macOS26.6.2 arm64, 설치된 Chrome156, Docker 프로젝트 `wbb-media-spike`, MediaMTX `1.21.3-ffmpeg`, CPU libx264/AAC, 로컬 hls.js1.6.7. Codex 실행은 Node26.13.0·Go1.27.1을 사용했다. 실제 화면·마이크·카메라 접근 없이 canvas·oscillator 및 브라우저 가짜 장치로 실행했다.

경로: 브라우저 합성 영상·음성 → WebRTC/WHIP → MediaMTX source → FFmpeg H.264/AAC → MediaMTX 단일 품질 LL-HLS → Chrome hls.js/MSE 재생. 모든 공개 포트는 loopback에 묶었다. 검증 HTML/JS는 Flutter 제품 UI가 아니다.

## 실제 실행

```sh
cd experiments/media-single-rendition
./start.sh
npm ci
npm run verify
# 별도 합성 송출을 시작한 상태에서:
./verify.sh
./stop.sh
```

검증기는 시스템 Google Chrome이 필요하다. 시작 시 H.264/AAC 지원을 검사하며 지원하지 않는 브라우저를 성공으로 처리하지 않는다. 검증 중 같은 source 경로에 다른 송출을 시작하지 않는다.

| 검사 | 관측 |
| --- | --- |
| WHIP·ICE | POST201, Location/ETag, PATCH204, ICE connected, 실제 송신 bytes/frames 증가 |
| LL-HLS | master→media playlist 탐색, 실제 part GET200·진행, 요청 msn/part가 포함된 blocking reload 응답200 |
| 실제 재생 | Codex 실행에서 시간15.13→17.59초, 디코딩 프레임4→79 증가; 브라우저 오디오 비영 신호 확인 |
| 출력 | ffprobe H.264/AAC·1280×720; AAC→PCM 복호화 비영 신호. 별도 verify.sh도 codec·양축 치수를 assert |
| 종료 | 원래 인코더의 컨테이너/PID/시작 시각을 확보한 뒤 소멸 확인. source·output 비활성, WebRTC 세션0, 6초 관측창의 각 표본에서 재등장·새 part 진행 없음 |
| 재시작·경합 | 시작→중지→재시작, 새 방송 연결 후 이전 POST201 응답을 해제하는 barrier, 깨진 SDP, 모의 ended, 캡처 대기 중 취소·늦은 허용 |
| PC 오디오 모의 | 공유 소리와 가짜 마이크의 혼합이 비영이며 감쇠 후 피크 상한을 지킴. 실제 두 음원 청취와 구분 |
| 부분 실패 | 합성·PC destination 생성 이후 오류를 주입하고 원본 및 출력 트랙 ended·AudioContext closed·송출 없음 확인 |
| 플레이어 실패 | native 실패·MSE 불가의 정확한 오류와, MSE 가능 시1회 전환 후 실제 재생을 실패 주입으로 검증. Safari 검증 아님 |

로컬 PeerConnection을 먼저 닫으면 MediaMTX가 세션을 먼저 회수해 DELETE404가 될 수 있다. 해당 응답과 실제 서버 세션0을 함께 검사한다. 일반 DELETE 실패를 모두 성공으로 계산하지 않는다. 재시도 루프의 새 FFmpeg와 원래 인코더를 구분하며 조회 실패·네트워크 timeout을 종료 증거로 세지 않는다.

이전 독립 필터 검사에서는1080×1920→406×720,640×360→640×360,1280×1024→900×720을 확인했다. helper의 wildcard·비loopback 바인딩 거절, npm 의존성 audit0건도 확인했다. 이번 변경은 해당 필터·helper·의존성을 바꾸지 않았다.

## 명시적 ingress 종료 독립 검사

Codex가 별도 Chrome 합성 송출을 시작한 뒤 `verify.sh`로 H.2641280×720/AAC를 확인했다. source와 output이 모두 ready인 상태에서 source의 WebRTC 세션 ID와 세션 목록을 대조하고 `POST /v3/webrtc/sessions/kick/{id}`를 호출했다. 응답200 후 두 path가 ready=false·source=null, WebRTC 세션 수0이 됐고 같은 ID의 재호출은404였다. API·본문 timeout 또는 조회 실패는 성공으로 처리하지 않았다.

경로는 [MediaMTX1.21.3 API 등록 코드](https://github.com/bluenviron/mediamtx/blob/v1.21.3/internal/api/api.go)를 확인했다. 이는 loopback 관리 API의 종료 기능 검증이며, 제품 서버의 권한 검사·종료 명령 전달·상태 영속화 통합은 티켓04 범위다. 첫 독립 시도는 보조 HTTP 서버 종료로 접속에 실패했고, 서버를 전경 세션으로 유지한 재실행에서 위 결과를 얻었다.

`verify.sh`의 실패 판정도 별도로 검사했다. stub 입력의 정상 manifest/코덱은 exit0, CAN-BLOCK-RELOAD 누락·관리 API 실패·AAC 누락은 각각 exit1이었다. curl 요청은 제한 시간을 가지며 API 조회와 JSON 검사를 파이프로 묶지 않아 앞단 실패를 숨기지 않는다.

## 수정 근거와 한계

이전 체크포인트 c37ad37은50통과·1건 건너뜀·재생2실패였다. 기본 Chromium141의 H.264 미지원과 Chrome의 native HLS 선택 오류를 분리해 Chrome/MSE 우선 선택으로 수정했다. 진단용 강제 경로가 아니라 현재 구현 자체를 독립 실행한 결과가 위83항목이다. [리뷰 기록](../reviews/02-media-spike.md)에 실패 정리·검증기의 수정 근거를 남겼다.

실제 PC 화면·공유 소리·마이크 청취, 휴대폰 카메라·마이크, 다른 물리 기기 시청, 외부망 ICE/NAT/TURN, Safari HTTPS LL-HLS, Labtop VAAPI, 다중 품질 ABR, 장시간·부하 용량, 제품 인증·Flutter 통합, 운영 배포는 미검증이다. 브라우저 analyser/서버 PCM의 비영 신호는 실제 스피커 청취를 대신하지 않는다.

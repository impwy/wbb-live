# 단일 품질 미디어 실험 — 로컬 검증

브라우저 WebRTC/WHIP → MediaMTX → CPU FFmpeg H.264/AAC → 단일 품질 LL-HLS를 로컬에서 확인하는 티켓02 실험이다. 제품 Flutter/API와 연결하지 않았으며, **로컬 합성·모의 검증83항목은 통과했으며 실제 기기·외부망 관문은 미완료다.**

## 환경과 실행

Go1.27 이상, Docker Compose, 시스템 Google Chrome(H.264/AAC 지원), Node.js, npm, `curl`, `lsof`, Python3가 필요하다. macOS arm64에서 실행했다. 이미지 `bluenviron/mediamtx:1.21.3-ffmpeg`와 로컬 번들 hls.js1.6.7을 사용한다. 런타임 CDN 요청은 없다.

```sh
cd experiments/media-single-rendition
./start.sh
# 자동 검사 전에 다른 송출을 시작하지 않는다.

npm ci              # 검증기는 시스템 Chrome(channel:'chrome') + H.264/AAC 사전검사 사용
npm run verify

# 위 자동 검사가 끝난 뒤 웹 화면에서 합성 송출을 다시 시작하고 별도 터미널에서:
./verify.sh

./stop.sh
```

검증기는 시스템 Chrome(`channel:'chrome'`)을 사용하며 기동 시 H.264/AAC `canPlayType` 사전검사를 통과해야 한다. 기본 번들 Chromium은 H.264 미지원으로 즉시 중단한다. 재생은 MSE(hls.js) 경로를 우선 사용하고 native HLS는 MSE를 지원하지 않는 환경에서 시도한다(실패 주입 11a/11b로 폴백·정확한 오류 안내만 검증, Safari native HLS는 미검증).

포트는 모두127.0.0.1에만 공개한다: 웹8090, HLS8888, WHIP8889, 관리API9990, RTSP8554, ICE UDP18389. 관리·송출 인증이 없는 실험이므로 외부 배포용 구성이 아니다. 휴대폰이나 다른 컴퓨터가 접속하는 구성도 아직 아니다. 기존 서비스와 분리된 Docker 프로젝트 `wbb-media-spike`를 사용한다.

합성 모드는 canvas와 oscillator만 사용한다. 실제 화면·마이크·카메라는 해당 버튼을 직접 눌렀을 때만 요청한다. PC 마이크 대기 중 공유 중지 경합·혼합 감쇠 gain·후속 초기화 실패 정리는 자동 회귀(S7~S12)로 검증했지만 실제 기기 UX가 아니므로 실제 PC·휴대폰 기능의 인수 완료로 간주하지 않는다.

## 현재 증거와 남은 검증

- 최종 자동 검증(합성·모의 기반): **83항목 전부 통과, 0 skip, 0 실패**(코덱 사전검사 포함). pi와 Codex가 별도로 실행해 확인했다.
- WHIP201/PATCH204, 실제 ICE·송신 증가, MSE 재생 시간·프레임·오디오 비영(서버 PCM 포함), H.264/AAC 출력, LL-HLS part 다운로드·진행·blocking reload(요청 msn/part 실제 포함 판정)을 확인했다.
- 화면1280×720 출력, 세로 영상 축소, 작은 입력 무확대를 확인했다. CPU 결과이며 VAAPI가 아니다.
- 종료 검증: 정지 전 출력 준비·인코더 실재 확인 후, 원래 FFmpeg PID(컨테이너+PID+/proc starttime) 소멸, 출력 path 비활성, 6초 관측 창 동안 매 표본마다 인코더 부재·API 비활성·새 part/sequence 부재(200은 MEDIA-SEQUENCE/PART 유효성 검사 후 고정 여부 비교)를 재확인한다.
- 종료·재시작과 비동기 경합(늦은 POST·캡처·ended·믹스 실패·dest 트랙 누수) 회귀를 포함한다.

정확한 결과는 [검증 기록](../../docs/verification/02-media-spike.md), 수정 목록은 [리뷰](../../docs/reviews/02-media-spike.md), 전체 관문은 [티켓02](../../.scratch/wbb-live/issues/02-media-single-rendition-spike.md)를 따른다. 실제 두 기기 영상·음성, 외부망/NAT/TURN, Safari HTTPS, VAAPI·ABR는 미검증이다.

## 출처

- [MediaMTX](https://github.com/bluenviron/mediamtx), [FFmpeg](https://ffmpeg.org/): 고정 MediaMTX Docker 이미지 안의 실행 도구.
- [hls.js v1.6.7](https://github.com/video-dev/hls.js/tree/v1.6.7): `web/vendor/hls.min.js`, 배포본 고지 `web/vendor/hls.js.LICENSE.txt`.
- [Playwright](https://github.com/microsoft/playwright): 검증 도구1.56.1, `package-lock.json`으로 고정.

이 문서는 Codex가 실제 산출물과 실패 결과를 기준으로 작성했다. 후속 구현과 실제 환경 검증은 티켓 순서를 따른다.

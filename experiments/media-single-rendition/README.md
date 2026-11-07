# 단일 품질 미디어 실험 — 검토 중

브라우저 WebRTC/WHIP → MediaMTX → CPU FFmpeg H.264/AAC → 단일 품질 LL-HLS를 로컬에서 확인하는 티켓02 실험이다. 제품 Flutter/API와 연결하지 않았으며, **현재 플레이어 자동 선택에 결함이 있어 완료 상태가 아니다.**

## 환경과 실행

Go1.27 이상, Docker Compose, Node.js, npm, `curl`, `lsof`, Python3가 필요하다. macOS arm64에서 실행했다. 이미지 `bluenviron/mediamtx:1.21.3-ffmpeg`와 로컬 번들 hls.js1.6.7을 사용한다. 런타임 CDN 요청은 없다.

```sh
cd experiments/media-single-rendition
./start.sh
# http://127.0.0.1:8090 에서 합성 모드 시작

npm ci
npx playwright install chromium
npm run verify

./stop.sh
```

현재 기본 Chromium141은 H.264를 지원하지 않아 위 검사의 재생2항목이 실패한다. Chrome156에서는 코덱을 지원하지만 현재 앱의 native HLS 선택이 실패한다. Chrome의 hls.js/MSE 재생과 실행기 코덱 사전 검사 수정은 GLM 사용 한도 복구 후 진행한다. 이 명령을 정상 통과한 검사로 인용하지 않는다. 별도 `verify.sh`도 master/variant 해석 오류가 있어 수정 대상이다.

포트는 모두127.0.0.1에만 공개한다: 웹8090, HLS8888, WHIP8889, 관리API9990, RTSP8554, ICE UDP18389. 관리·송출 인증이 없는 실험이므로 외부 배포용 구성이 아니다. 휴대폰이나 다른 컴퓨터가 접속하는 구성도 아직 아니다. 기존 서비스와 분리된 Docker 프로젝트 `wbb-media-spike`를 사용한다.

합성 모드는 canvas와 oscillator만 사용한다. 실제 화면·마이크·카메라는 해당 버튼을 직접 눌렀을 때만 요청한다. 화면 공유의 마이크 권한 대기 중 공유 중지 경합과 혼합 gain 보완이 남아 있으므로 실제 PC·휴대폰 기능의 인수 완료로 간주하지 않는다.

## 현재 증거와 후속 수정

- 검증53항목:50통과, 브라우저 오디오1건 건너뜀, 재생2실패.
- WHIP201/PATCH204, 실제 ICE·송신 증가, H.264/AAC 출력, LL-HLS part 다운로드·진행을 확인했다.
- 화면1280×720 출력, 세로 영상 축소, 작은 입력 무확대를 확인했다. CPU 결과이며 VAAPI가 아니다.
- 종료·재시작과 일부 비동기 경합을 검사했다. FFmpeg 원래 PID 종료·출력 경로 종료와 새 part 중단 검증은 남았다.
- Codex의 진단용 MSE 강제 실행에서 영상 재생은 확인했지만 이 변경은 구현에 반영되지 않았다.

정확한 결과는 [검증 기록](../../docs/verification/02-media-spike.md), 수정 목록은 [리뷰](../../docs/reviews/02-media-spike.md), 전체 관문은 [티켓02](../../.scratch/wbb-live/issues/02-media-single-rendition-spike.md)를 따른다. 실제 두 기기 영상·음성, 외부망/NAT/TURN, Safari HTTPS, VAAPI·ABR는 미검증이다.

## 출처

- [MediaMTX](https://github.com/bluenviron/mediamtx), [FFmpeg](https://ffmpeg.org/): 고정 MediaMTX Docker 이미지 안의 실행 도구.
- [hls.js v1.6.7](https://github.com/video-dev/hls.js/tree/v1.6.7): `web/vendor/hls.min.js`, 배포본 고지 `web/vendor/hls.js.LICENSE.txt`.
- [Playwright](https://github.com/microsoft/playwright): 검증 도구1.56.1, `package-lock.json`으로 고정.

이 문서는 Codex가 실제 산출물과 실패 결과를 기준으로 작성했다. 추가 구현은 지정 pi GLM의 사용량 제한이 해제된 뒤 이어간다.

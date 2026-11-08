# 02 VAAPI 후속 검증

검증일2026-11-08. 코드 기준 `813e0b9`, 독립 검토 스냅샷 `ba2050f`. 구현은 기존 pi GLM, 독립 실행·리뷰는 Codex다. **미디어 경로의 부분 성공과 자동 검사 전체 통과를 구분하며, 현재 수정 대기다.**

## 환경

로컬 macOS arm64·시스템 Chrome156·Node26.13.0, 원격 Linux x86_64·Intel Alder Lake-P Iris Xe·`renderD128`을 사용했다. MediaMTX1.21.3-ffmpeg, 별도 Alpine3.24 GPU 이미지, FFmpeg8.1.3-r0, intel-media-driver26.2.1-r0, libva2.23.0-r0이다. 원격 호스트 FFmpeg9.0.2의30프레임 preflight와 컨테이너 인코딩 검사를 구분했다.

고정 기반 Alpine digest는 `3f4d6b32e26703c6082839234f43769d5046b3ac7c910033911d1e71d8cd630a`, MediaMTX digest는 `208e9f1740a09a5ee63b6308e77ce0d54fce0456462f640e6bd9acfc62ebb5a2`다. 이번 GPU 빌드의 이미지 ID는 `fefe74d5d4d9e1f1c58b3a087ca4e1a794014423421bd8035ccb2e11d448b32f`였다. 설치 패키지 버전은 위와 같으며 apk 저장소 전체를 동결한 재현성 보장은 아니다.

모든 공개 매핑은 loopback, ICE는TCP18390만 사용했다. 로컬의 SSH 포워딩을 통해 원격 WHIP/HLS와 ICE에 연결했으며 실제 외부망 UDP/NAT/TURN 검증이 아니다. 장치 입력은 canvas/oscillator 또는 서버의 testsrc2/sine 합성이다.

## 확인한 결과

| 검사 | Codex 관측 |
| --- | --- |
| 컨테이너 preflight | iHD26.2.1 초기화, vainfo exit0, 실제30프레임 h264_vaapi 인코딩 성공. stock MediaMTX FFmpeg 이미지의 드라이버 부재는 exit251로 재현 |
| 해상도3종 | 실제 입력과 출력 모두 확인:1280×720→1280×720,1080×1920→406×720,640×360→640×360. H.264/AAC·양축 제한·무확대 확인 |
| 브라우저 실제 경로 | WHIP 송출, 선택된 양쪽 ICE candidate가TCP, GPU 출력 H.264/AAC1280×720 |
| 실제 재생·음성 | 독립 probe 시간13.503699→15.881128초, 디코딩 프레임0→76, 브라우저 오디오 비영2218. 서버 PCM576000B·peak7343 |
| 최신 Python 종료 검사 | 별도 독립 명령으로 before→브라우저stop→after. 실제 원래PID/시작 시각 확보, part 진행3→4, 이후 source/output 비활성·원래 encoder 소멸·최소6초의4표본·세션0 확인 |
| 모의 오류 주입 | selftest11/11 통과. 실제 기기/서버 흐름이 아니며, 실제 상태 파일을 공유하는 결함은 별도 지적. 현재 Docker 실패 사례는 먼저 compose ps에서 실패하므로 원래PID exec 지점의 오류 주입을 추가해야 함 |
| 문법·패키지 | Python AST·shell·JS 문법, npm ci 성공, 이번 npm 의존성 audit0건. 다른 시스템 패키지 전체 취약점 검증은 아님 |

## 실패와 증거의 한계

pi는 브라우저 검사13/13·exit0을 보고했으나, verifier가 삭제된 `verify-termination.sh`를 계속 호출했고 원격 덮어쓰기 디렉터리에는 옛 파일이 남아 있었다. Codex는 기존 폴더를 증거로 보관하고 **새 빈 디렉터리에 고정 스냅샷만 전송**했다. `.sh` 부재를 확인한 뒤 같은 verifier를 실행하니 미디어 검사11개 이후 종료의 before 호출에서 원격exit127, 전체exit1이 됐다.

Codex가 최신 `.py`를 직접 호출한 별도 검증 명령의 성공은 **자동 검사 호출부를 수정하거나 전체13항목을 통과시킨 결과가 아니다.** 호출부 연결, 음성 관측 누락의 집계, 시작 신호 정리, selftest 상태 격리, preflight 기반 이미지 조회를 수정해야 한다. [리뷰 기록](../reviews/02-media-vaapi.md)을 따른다.

시작 스크립트는 Codex의 로컬 외부 명령 stub 검사에서 build 중 SIGTERM→cleanup down→up transcoder→exit0이 재현됐다. 실제 서버에 종료 신호를 주어 기존 서비스를 위험에 노출한 검사는 아니다. preflight는 이번 서버에 기반 이미지 태그가 이미 존재한 환경에서 성공했으며 깨끗한 BuildKit 태그 부재 문제를 해결한 증거가 아니다.

## 정리와 재개

검사 후 원격 전용 컨테이너0개, 로컬 helper/SSH forward 종료, 로컬8090/8888/8889/18390 포트 해제를 확인했다. 비교 기준으로 기록한 원격 기존 컨테이너16개는 같은ID로 계속 실행 중이었다. 사용자 서비스의 모든 HTTP 기능을 인수 검사했다는 뜻은 아니다. 원격 실험 파일·로그는 후속 비교용으로 보존했다.

GLM이429/1308과 `2026-11-08 17:36:09`를 반환했다(시간대 미표기). 기존 pi 세션에서 실제 응답으로 한도 해제를 확인한 뒤 리뷰 지적을 수정한다. Google 로그인 상세 계약은 작성됐지만 구현은 아직 시작하지 않았다.

수동 절차는 [PC·모바일 테스트 안내](02-manual-testing.md)와 [GPU 실험 README](../../experiments/media-vaapi/README.md)를 따른다. 실제 PC 두 음원 청취·모바일 카메라·다른 물리 기기·외부망·Safari HTTPS·장시간/부하·ABR·제품 인증/Flutter 통합·운영 배포는 미검증이다. 티켓02 전체는 계속 in-progress다.

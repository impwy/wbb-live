# VAAPI 단일 품질 실험 — 검토 중

티켓02 후속이다. 기존 CPU 실험의 웹 helper를 재사용해 Chrome 합성 WHIP → SSH TCP ICE → Labtop의 Intel GPU H.264/AAC 변환 → LL-HLS를 검사한다. 제품 Flutter·인증 통합 및 운영 배포가 아니다.

**현재 미완료:** 브라우저 자동 검증기가 삭제된 `verify-termination.sh`를 참조한다. 원격에 남은 옛 파일을 이용한 pi의13/13 결과는 깨끗한 현재 코드의 전체 통과 증거가 아니다. 종료 검사는 `verify-termination.py`로 바뀌었으며 호출부 수정·독립 재실행이 필요하다. 상세 지적과 상태는 [리뷰](../../docs/reviews/02-media-vaapi.md)를 따른다.

## 구성과 준비

- 원격: Linux x86_64, Intel `/dev/dri/renderD128`, Docker/Compose, Python3, curl, ss. 호스트 패키지를 변경하지 않고 VAAPI 장치만 transcoder에 전달한다. privileged/host networking을 사용하지 않는다.
- 로컬: Go, 시스템 Google Chrome(H.264/AAC), Node.js/npm, SSH. 기존 `media-single-rendition`의 웹 화면을 그대로 사용한다.
- SSH 별칭 `Labtop`은 사용자 로컬에서 설정한다. 실제 주소·계정·키는 저장소에 넣지 않는다. 현재 브라우저 검증기는 이 별칭과 원격 `/tmp/wbb-media-vaapi` 경로를 사용한다.
- Docker 프로젝트는 `wbb-media-vaapi`다. 원격 TCP18888(HLS),18889(WHIP),19990(API),18554(RTSP),18390(ICE)는 모두127.0.0.1에만 공개된다. 기존 서비스·Caddy·DNS·방화벽·k3s는 변경하지 않는다.
- MediaMTX1.21.3-ffmpeg 및 Alpine3.24 기반 이미지는 파일의 digest로 고정했다. `apk` 패키지는 빌드 시 설치하므로 정확한 버전은 preflight 기록을 함께 보존한다. 실제 검증한 Intel iHD는26.2.1이다. GPU decode는 가정하지 않으며 GPU 인코딩 실패를 CPU로 자동 대체하지 않는다.

## 원격 준비

이전 파일이 섞이지 않는 디렉터리에 복사해야 한다. 기존 디렉터리가 있으면 진행을 멈추고 그 실행의 컨테이너를 종료한 뒤 파일과 증거를 보관한다. 삭제된 파일이 남는 덮어쓰기 복사로 통과를 주장하지 않는다.

```sh
ssh Labtop 'test ! -e /tmp/wbb-media-vaapi && mkdir /tmp/wbb-media-vaapi'
# 위 명령이 성공한 경우에만:
tar --exclude=node_modules --exclude=__pycache__ --exclude=logs   -C experiments/media-vaapi -cf - . |   ssh Labtop 'tar -xf - -C /tmp/wbb-media-vaapi && mkdir -p /tmp/wbb-media-vaapi/logs'
ssh Labtop 'cd /tmp/wbb-media-vaapi && ./preflight.sh'
ssh Labtop 'cd /tmp/wbb-media-vaapi && ./start.sh'
```

현재 preflight의 별도 `alpine:3.24` 로컬 태그 조회는 깨끗한 BuildKit 환경에서 실패할 수 있어 수정 대기 중이다. 실패 시 성공으로 취급하거나 다음 명령을 계속하지 않는다.

서버 내부 합성 검사(실제 브라우저 WHIP와 별도):

```sh
ssh Labtop 'cd /tmp/wbb-media-vaapi && docker compose --profile source up -d source'
# 출력이 준비된 후:
ssh Labtop 'cd /tmp/wbb-media-vaapi && ./verify.sh'
ssh Labtop 'cd /tmp/wbb-media-vaapi && ./verify-resolutions.sh'
```

해상도 검사는1280×720,1080×1920,640×360 입력을 실제 확인하며, 기대 출력은1280×720,406×720,640×360이다. 이 검사 후 source 컨테이너가 제거됐는지 확인한 뒤 브라우저 송출을 시작한다.

## 브라우저 수동 검사

별도 로컬 터미널에서 SSH 포워딩을 전경으로 유지한다. CPU 실험과 로컬 포트가 겹치므로 동시에 실행하지 않는다.

```sh
ssh -N -o ExitOnForwardFailure=yes   -L 127.0.0.1:8889:127.0.0.1:18889   -L 127.0.0.1:8888:127.0.0.1:18888   -L 127.0.0.1:18390:127.0.0.1:18390 Labtop
```

다른 터미널에서 웹 helper만 실행한다. CPU 실험의 start.sh는 실행하지 않는다.

```sh
cd experiments/media-single-rendition
go build -o bin/spike-server .
"$PWD/bin/spike-server"
```

Chrome에서 `http://127.0.0.1:8090`을 연다. [수동 테스트 방법](../../docs/verification/02-manual-testing.md)의 합성 또는 PC 절차를 따른다. 이 SSH 경로는 UDP·실제 외부망 NAT/TURN·모바일 HTTPS 검증이 아니다. 실제 카메라/마이크/화면 선택은 사용자가 직접 수행한다.

## 자동 검사와 종료 증거

`python3 selftest-termination.py`는 모의 실패 주입 검사다. 현재 실제 verifier와 상태 파일 경로를 공유하는 결함이 있으므로 실제 before/after 사이에 실행하지 않는다. 별도 임시 상태 경로로 분리하는 수정이 필요하다.

브라우저 자동 검사 준비는 이 폴더에서 `npm ci` 후 `npm run verify`다. 현재 호출부 결함으로 깨끗한 환경의 마지막 종료 검사가 실패하므로 전체 성공으로 표시하지 않는다. pi가 생성한 package-lock은 Codex가 게시 대상에 포함했다.

기존 Python 종료 검사를 별도로 확인하려면 활성 송출 중 다음 before를 수행하고, **그 뒤** 브라우저에서 중지한 후 after를 실행한다.

```sh
ssh Labtop 'cd /tmp/wbb-media-vaapi && python3 verify-termination.py before'
# 브라우저에서 송출 정지
ssh Labtop 'cd /tmp/wbb-media-vaapi && python3 verify-termination.py after'
```

검사 완료 후 원격 전용 스택을 내린다. 전경 helper와 SSH 포워딩 터미널은 각각 Ctrl+C로 종료한다. 다른 SSH·서비스·컨테이너를 패턴으로 일괄 종료하지 않는다.

```sh
ssh Labtop 'cd /tmp/wbb-media-vaapi && ./stop.sh'
ssh Labtop 'cd /tmp/wbb-media-vaapi && docker compose --profile source ps -aq'
```

## 한계와 출처

실제 PC 두 음원 청취·모바일 카메라·다른 물리 기기 시청·외부망·Safari HTTPS·30분 이상 지속/부하·ABR는 미검증이다. 짧은 단일 GPU 경로 성공으로 동시10개 방송이나 장기 운영 용량을 보장하지 않는다.

- [MediaMTX1.21.3](https://github.com/bluenviron/mediamtx/tree/v1.21.3), [설정 참조](https://mediamtx.org/docs/references/configuration-file): WHIP, TCP ICE, LL-HLS.
- [FFmpeg](https://ffmpeg.org/), [Intel media driver](https://github.com/intel/media-driver): H.264 VAAPI 인코딩.
- [Alpine](https://alpinelinux.org/): 격리된 FFmpeg/드라이버 실행 이미지.
- hls.js1.6.7과 Playwright1.56.1은 기존 CPU 실험의 출처·버전 기준을 따른다.

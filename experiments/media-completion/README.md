# MediaMTX 종료 완료 증거 격리 실험 (진행 중)

이 디렉터리는 07-A1의 **격리 실험**이다. 제품 MediaMTX, API, worker와 기존 미디어 스택에는 적용하지 않았다. 고정 MediaMTX에서 두 방송의 WHIP→RTSP→FFmpeg→HLS, 장벽별 종료, 응답 유실, Linux 프로세스 종료·재시작, 영속 이력 및 용량 경계를 검사했다. 아래의 로컬 검사 통과는 제품 DB·worker 통합이나 실제 기기 인수를 뜻하지 않는다.

## 고정 원본과 패치

- 원본: MediaMTX `v1.21.3`, commit `9540cb6c1d9f1e9351fcc4ded3afc249ffff22bc`.
- 라이선스: 동봉한 [LICENSE](LICENSE) (원본 MIT).
- 변경: [upstream.patch](upstream.patch). 새 실험용 `internal/wbbcompletion`과 실제 WebRTC·경로·인증·API 종료 경계 수정이 포함된다.

재현용 원본 확인과 패치 적용:

```sh
git clone --depth 1 --branch v1.21.3 https://github.com/bluenviron/mediamtx.git /tmp/wbb-mediamtx-spike
test "$(git -C /tmp/wbb-mediamtx-spike rev-parse HEAD)" = 9540cb6c1d9f1e9351fcc4ded3afc249ffff22bc
git -C /tmp/wbb-mediamtx-spike apply --check "$PWD/experiments/media-completion/upstream.patch"
git -C /tmp/wbb-mediamtx-spike apply "$PWD/experiments/media-completion/upstream.patch"
```

이미지는 [Dockerfile](Dockerfile)의 고정 Go/MediaMTX 이미지 digest로 빌드한다. Docker 빌드는 `VERSION`을 생성하고 `go generate ./internal/servers/hls ./internal/staticsources/rpicamera`로 원본 자산을 만든다. 생성물은 패치에 포함하지 않는다.

## 격리 실행

전용 loopback 포트는 `29554`, `29697`–`29699`, `29791/udp`, `29888`–`29889`다. 시작 전에 충돌과 기존 방송 부재를 확인한다. `anchor.json`에는 실험용 `slot`과 `root`를 담고, `.env`에는 `WBB_COMPLETION_KEY_HEX` 32바이트 난수의 hex 표현만 담는다. 두 파일은 로컬 비추적 입력이며 값을 로그·패치에 기록하지 않는다. 빈 볼륨은 자동 초기화하지 않는다.

```sh
cd experiments/media-completion
docker build -t wbb-media-completion-spike:local .
docker volume create wbb-media-completion-spike_completion_state
docker run --rm --network none --env-file .env \
  -e WBB_COMPLETION_ANCHOR=/spike/anchor.json \
  -e WBB_COMPLETION_DIR=/spike/state \
  -v "$PWD/anchor.json:/spike/anchor.json:ro" \
  -v wbb-media-completion-spike_completion_state:/spike/state \
  --entrypoint /wbb-bootstrap wbb-media-completion-spike:local
docker compose -p wbb-media-completion-spike --env-file .env -f compose.yml up -d
node verify-boundary.mjs
node --test boundary-cleanup.test.mjs
node verify-media.mjs
node verify-media.mjs before_add_publisher
node verify-media.mjs during_rtp_callback
node verify-media.mjs kill_restart
node verify-capacity-epoch.mjs
node verify-active-capacity.mjs
```

나머지 미디어 모드는 `before_register`, `before_session_initialize`, `during_auth`, `before_completion_response`, `during_pli`, `during_ice_patch`, `lost_close`, `cancel_post`다. 각 실행은 자기 회차·브라우저·FFmpeg를 정리한다. `kill_restart`는 전용 MediaMTX 컨테이너만 종료·재시작하므로 다른 모드와 동시에 실행하지 않는다. `verify-capacity-epoch.mjs`는 별도 일회용 볼륨과 포트를 사용해 자체 정리한다. 브라우저 검증에는 `../media-single-rendition/node_modules/playwright`와 H264 지원 Chrome이 필요하다.

## 현재 검증 범위

임시 원본 클론에서 registry의 일곱 영속화 지점 SIGKILL, A→B→C 실행 교체, 잠금 inode·내용 변조, 장벽, HTTP 귀속·서명 마스킹, 취소된 세션 소유권, ICE UDP/TCP 초기화 충돌을 검사했다. `go test -race -count=1`로 관련 패키지 회귀를 통과했고 Linux/arm64 이미지에서 WebRTC candidate 회귀도 통과했다. `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go vet ./internal/wbbcompletion ./internal/protocols/httpp ./internal/protocols/webrtc ./internal/servers/webrtc ./internal/core ./internal/api ./cmd/wbb-bootstrap`가 통과했다. 초기화 충돌은 HTTP 리스너를 ICE 리스너 준비 뒤에 열고, 실패 시 앞서 연 UDP/TCP를 회수하는 경계다. Core의 이후 자원 초기화 실패 경로는 실험 장벽을 abort한 뒤 기존 자원을 닫는다.

전용 Compose의 MediaMTX와 인증 fixture만 기동한 뒤 `node verify-media.mjs`를 실행한 결과, 두 Chrome 송출·두 시청, H264/Opus 입력, RTSP→FFmpeg→HLS 경로를 거쳐 A 종료가 완료됐다. A의 FFmpeg 프로세스 종료와 source/output·ingress 부재, B의 FFmpeg 신원·lifecycle tuple/epoch 유지, B의 연속 8구간 영상·음성·수신 바이트 증가를 검사했다. B도 종료하고 전용 컨테이너·인증 fixture 허용 항목·Chrome 자원을 정리한 뒤 종료 코드 0이었다. 로그: `/tmp/wbb-07a1-media-smoke-v5.log` (로컬 임시 검사 로그, 게시 산출물 아님). 이 실행은 **정상 종료 스모크**로만 분류한다.

같은 전용 스택에서 `node verify-media.mjs <stage>`로 등록 전·세션 초기화 전·인증 중·AddPublisher 전·완료 응답 전 장벽을 각각 검사했다. 유효 SDP의 A를 해당 장벽에 고정하고 close→closing(등록 전은 completed)→명시 release→completed를 확인했다. RTP callback·PLI·ICE PATCH의 비동기 작업 중 장벽에서도 close가 조기 completed를 반환하지 않았고, 해제 후 A 미디어·FFmpeg가 사라졌다. 동시에 B의 H264/Opus 송출·FFmpeg·HLS 시청이 구간별 영상·음성·새 미디어 수신을 유지했고, B의 PID·시작 시각 및 epoch tuple도 유지됐다. 각 실행은 정리 검증까지 종료 코드 0이었다. 로컬 로그: `/tmp/wbb-07a1-media-barrier-before-register.log`, `...-before-session.log`, `...-during-auth.log`, `...-before-add-final-image.log`, `...-before-response.log`, `/tmp/wbb-07a1-media-during-rtp.log`, `...-during-pli.log`, `...-during-ice-patch-v3.log`.

`node verify-media.mjs lost_close`는 A가 실제 송출 중 관리 close의 upstream 202를 확인하고 클라이언트 응답만 유실시킨 뒤 query로 completed를 복구했다. `node verify-media.mjs cancel_post`는 응답 전 장벽에 도달한 A의 WHIP 클라이언트를 취소하고 A의 종결 및 B의 연속 수신을 확인했다. 둘 다 종료 코드 0이며 로컬 로그는 `/tmp/wbb-07a1-media-lost-close.log`, `/tmp/wbb-07a1-media-cancel-post-v3.log`다. 응답 유실 이후 **알 수 없는 Location의 영속 회수 정책**은 제품 통합 범위에 남는다. 첫 `cancel_post` 시도는 B가 Opus 한 트랙만 받아 변환 준비에 실패했으며 A 취소 경계에 도달하지 않았다. 이후 검증기는 영상 RTP 선행 전송과 MediaMTX의 H264·Opus 두 트랙 확인을 추가했다.

실험 모드에서는 upstream HTTP 디버그 로그가 SDP/ICE 및 인증 callback 본문을 기록할 수 있으므로 `logLevel: debug`와 `dumpPackets: true`를 시작 전에 거절한다. 신규 HMAC 서명 헤더는 대소문자 변형도 로그에서 마스킹한다. Linux 단위 검사로 두 설정 거절을 확인했다.

`node verify-media.mjs kill_restart`는 실제 MediaMTX 프로세스 종료 뒤 같은 볼륨에서 새 임의 epoch와 증가한 순서를 확인했다. 이전 A/B 작업은 정확한 이력을 근거로 completed가 됐고 두 옛 FFmpeg·미디어 자원은 사라졌다. 공유 MediaMTX 재시작 중 B의 연속 재생은 주장하지 않는다. 로컬 로그: `/tmp/wbb-07a1-media-kill-restart.log`.

`node verify-capacity-epoch.mjs`는 빈 볼륨 기동 거절, 동시 잠금 거절, 용량 2 포화의 `capacity_unknown`과 기존 tombstone 보존, 실제 A→B→C 프로세스 종료·재시작을 확인했다. debug/packet dump 및 global/path hook 설정도 기동 전에 거절됐다. 로컬 로그: `/tmp/wbb-07a1-capacity-epoch-v3.log`. 파일 watcher는 completion 설정에서 생성하지 않으며 동적 설정 변경을 지원하지 않는다. 설정 파일을 바꿨을 때 무변경을 관찰하는 별도 동적 실행 검사는 아직 하지 않았다.

최종 후속 검사에서 `AlwaysAvailable`·`Forward`를 유효한 exact path와 path defaults에 각각 설정해 기동 거절 4건을 확인했다. 이력에는 현재 내용의 SHA-256 checksum을 저장하며, 유효 JSON·유효 hex epoch ID 한 글자만 바꾼 별도 프로세스의 기동을 거절했다. 기존 무checksum 이력은 자동 승격하지 않고 거절한다. checksum은 우발적 내용 손상 검출용이며 악의적인 이력 재작성·옛 볼륨 복제·롤백 방지를 보장하지 않는다. 로컬 로그: `/tmp/wbb-07a1-capacity-final-v2.log`.

`node --test boundary-cleanup.test.mjs`는 close·장벽 release·fixture revoke·자원 부재 확인 각각의 실패가 최종 실패가 되면서 다른 정리 단계도 계속 시도함을 확인했다. `node verify-boundary.mjs`의 실제 HTTP 재검사는 모든 소유 작업 completed와 WebRTC·RTSP 세션 및 경로 부재까지 확인하고 종료 코드 0이었다. 로컬 로그: `/tmp/wbb-07a1-boundary-cleanup-final.log`, `/tmp/wbb-07a1-boundary-http-final.log`.

`node verify-active-capacity.mjs`는 기존 Compose와 다른 일회용 볼륨·네트워크·loopback 포트에서 capacity=2로 시작한다. 실제 A·B의 H264/Opus WHIP, RTSP→FFmpeg→HLS와 두 익명 시청이 활성일 때 제3 작업 close가 `503/capacity_unknown`이었고 A/B query는 유지됐다. A의 RTP callback을 장벽에 잡은 close는 `closing`, 해제 후 `completed`였으며 B의 8개 구간 영상·음성·새 데이터와 FFmpeg PID/시작 시각·epoch가 유지됐다. B까지 종료하고 해당 컨테이너·볼륨·네트워크를 제거했다. 로컬 로그: `/tmp/wbb-07a1-active-capacity-final.log`.

현재 HMAC 서명, operation refcount, 프로세스 lock/epoch 및 장벽은 **실험 계약**이다. 제품 DB 귀속·worker 종료 정책·unknown POST 처리에 연결되지 않았다. 기존 공개 스택에 적용하거나 전체 완료 증거로 사용하지 않는다.

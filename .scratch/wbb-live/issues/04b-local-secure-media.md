# 04-B: 로컬 인증 미디어 송출·시청 통합

**Status:** ready-for-agent
**Blocked by:** 04-A 독립 리뷰 완료. 실제02 휴대폰/외부망 이전 로컬 구현은 사용자 승인.
**구현 담당:** 지정 대체 Codex 단독 구현. 조정자 설계·독립 리뷰.

## 목표

Flutter 로그인 소유자가 PC 화면·공유 오디오·마이크를 송출하고 별도 익명 브라우저가 단일 품질을 시청한다. 종료·로그아웃·실패 시 실제 ingress와 변환을 정리한다.04-B1 서버 경계,04-B2 Flutter 사용자 흐름을 순서대로 검토하며 둘 다 검증 전 로컬 송출·시청 완료로 표시하지 않는다. 실제 물리 기기·외부망·VAAPI·ABR는 별도 인수다.

## 04-B1: 서버 미디어 경계

- docs/design.md의 WHIP POST/PATCH/DELETE와 playback 계약. publish audience는 기존 발급기 `wbb-publish`로 일치시킨다. grant 서명/알고리즘/issuer/audience/owner/family/generation/jti/만료/DB 상태 검증, 생성 작업 영속 예약, 동일 grant 동시 POST 거절, publisher 대체 금지.
- resource의 동일 family 및 유효 access 검증, 정상 refresh 유지, STOPPING 이후 PATCH 거절과 정리 DELETE 허용. 별도 family 동일 owner는 stop API 사용. Location은 동일출처 불투명 resource로 재작성한다. 임의 upstream URL/redirect 금지, SDP/ICE 크기·Content-Type·ETag 처리 계약을 구현 전에 명시한다.
- MediaMTX 직접 WHIP·RTSP·관리 포트는 내부 전용. 미디어 인증에서 action/path/회차 확인, worker 내부 자격과 browser grant 분리. 공개 시청은 로그인/Basic Auth를 요구하지 않는다.
- 회차/generation별 source/output, DB lease와 실행 식별자를 가진 FFmpeg 감독. 로컬 CPU libx264720p 이하 fallback, 소스 확대 금지. API 재시작 이후 기존 프로세스 식별/회수 및 동시 실행 방지를 구체적으로 설계한다. Docker socket을 공개하거나 API에 임의 명령 실행 권한을 노출하지 않는다.
- LIVE 감시 포함. ingress+신선한 재생 가능 media/part 확인 후 LIVE. 준비60초, 최초 ingress 이후 유실15초. 상태/outbox 원자성 유지. STOPPING 권한 차단→ingress 종료→FFmpeg TERM/제한시간 KILL→미해결 생성 없음·실제 자원 없음 확인→ENDED. 실패/unknown은STOPPING 유지.
- 늦은201, upstream 응답 유실, pending 작업/API 재시작을 정리한다. Redis/Kafka 없이 내부 종료 수행.04-A 무자원 지름길을 실제 관측으로 교체한다.
- playback은 LIVE일 때만 회차별 외부 masterUrl. 미디어 바이트는 Caddy→MediaMTX, 모든 master/media/init/part/segment 요청에서 종료 회차 차단. Go는 제어/인가만 담당한다.

## 04-B2: Flutter

- 사용자 클릭 직후 화면 선택, 캡처 확보 후 회차/grant 생성. 공유 소리 누락 안내·마이크 혼합·gain·H264 preflight·muted 미리보기. 기존 실험의 검증된 수명 관리 재사용.
- grant는 메모리만 유지. 공유 중지/화면 이탈/로그아웃/실패에서 track/AudioContext/PeerConnection 및 회차 종료. 늦은 응답과 새 회차 경합 방지.
- 별도 익명 시청에서 준비 전 재생 비활성화, hls.js MSE/native fallback, 자동재생 안내, 재시도·라이브 이동, 사용자 pause 유지. 회차 변경/종료 시 이전 플레이어 정리.

## 검증·운영 경계

기존8090/CPU 미디어와 사용자 자원을 보존하고 새 전용 loopback Compose/포트/DB로 검사한다. 익명·타인·다른family·다른회차·종료grant·중복POST·refresh·logout/reuse를 HTTP+실제DB에서 검사한다. 실제MediaMTX/FFmpeg로 원래 ingress/PID 종료, 이전HLS URL 거절, 다른 회차 보존, 제어 장애STOPPING/복구 종료를 확인한다. 독립 Chrome 프로세스의 로그인 송출자/익명 시청자로 합성 영상·두 음원·decoded frames·종료를 검사하고 실제 캡처 인수와 구분한다. Go race/vet, Flutter analyze/test/web build, 소유 자원 정리.

고정spike/source, any publish, 무한 FFmpeg 루프, 모든 시청자Basic Auth를 제품으로 복사하지 않는다.04-B1 시작 시 감독 프로세스/재시작 식별, 미디어 인가, HTTP 세부 계약의 짧은 설계안을 조정자에게 보고해 확정한 뒤 구현한다.

# 04-B: 로컬 인증 미디어 송출·시청 통합

**Status:** in-progress
**Blocked by:** 04-A(done,03ee70c), Draft PR5의 backend/frontend CI 통과. 실제02 휴대폰/외부망 이전 로컬 구현은 사용자 승인.
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

## 04-B1 설계 결정

API와 분리한 단일 worker 컨테이너가 회차/generation별 FFmpeg 자식을 감독한다. API 재시작은 worker를 중단하지 않는다. DB에는 lease 소유자와 실행 ID, worker 실행 식별자, PID/프로세스 시작 시각을 보관한다. PID 번호나 lease 만료만으로 부재를 단정하지 않는다. 프로세스 확인·종료는 해당 worker의 PID namespace에서 수행하며 host Docker socket을 사용하지 않는다. worker 종료가 컨테이너 종료로 이어져 자식이 함께 회수되는 실행 형태를 사용하고 이를 실제 kill/restart로 검증한다. 새 worker는 과거 worker의 종료와 MediaMTX 연결 부재를 확인한 뒤 인수한다. 이를 증명할 수 없으면STOPPING을 유지한다.

MediaMTX 직접 포트는 내부 전용이며 생성 작업별 내부 자격과 worker read/publish 자격을 분리한다. 인증 callback은 내부 호출만 허용하고 action/protocol/정확한 회차·generation 경로를 검사한다. Caddy의 공개 HLS 경로에는 외부가 위조한 인가용 헤더를 신뢰하지 않는 forward_auth를 적용한다. 모든 master/media/init/part/segment GET/HEAD에서 회차/generation/LIVE를 검사하고 no-store를 적용한다. 이미 클라이언트가 받은 버퍼나 종료 직전 승인된 전송의 회수를 보장하지 않는다. 종료 이후 신규 요청 거절과 실제 송출/변환 중단을 검증한다.

WHIP은 RFC9725를 기준으로 고정 MediaMTX 버전에서 검증한 Content-Type/ETag/If-Match/Location을 명시한다. 생성 작업 예약 후 upstream POST하며 timeout/늦은201/응답유실은 미해결 작업으로 유지한다. 연결 조회는 회차/generation/작업에 한정하고 다른 회차를 정리하지 않는다. callback과worker 제어 endpoint는 인터넷에 노출하지 않는다.

추가 경합 기준: 증가하는 fencing token을 작업 할당·상태 반영·worker 내부 자격 검증에 포함한다. lease 만료만으로 이전 worker가 사라졌다고 판단하지 않으며 DB 단절 시 신규 시작을 거절하고 lease 갱신 실패 작업을 안전하게 종료한다. 이전 RTSP 연결은 자격 재검사에만 의존하지 않고 명시적으로 종료한다.

HLS 인가 직후 stop 및 대기 중 `_HLS_msn` blocking reload를 검사한다. 이미 승인된 요청은 회차 경로 제거/연결 취소 또는 명시한 유한 최대 대기시간으로 끝내며 no-store만으로 진행 중 응답 취소를 주장하지 않는다.

### 재시작 소유권 보완

동일 로컬 named volume의 고정 lock inode를 모든 worker가 사용하며 파일/볼륨 교체로 우회하지 않는다. 잠긴 동일 open-file-description의 FD를 FFmpeg에 ExtraFiles로 상속하고 실제 유지 여부를 검사한다. 명시적 LOCK_UN은 자식 FD의 잠금도 해제하므로 사용하지 않고 각 소유자는 close만 수행한다. 마지막 보유자 종료 후에만 새 worker가 배타 잠금을 획득한다. worker 수명이 끝나면 컨테이너 PID namespace도 끝나는 실행 구성을 실제 kill/restart로 검증한다. 구형 비상속 실행에는 이 증명을 적용하지 않는다. [Linux flock](https://man7.org/linux/man-pages/man2/flock.2.html), [PID namespace](https://man7.org/linux/man-pages/man7/pid_namespaces.7.html)의 동작을 근거로 하되 실제 고정 FFmpeg 이미지의 FD 유지가 필수 관문이다.

미해결 POST 잔존 상한을 입증하지 못하면 pending을 유지한다. client timeout을 서버 실행 상한으로 사용하지 않는다. 생성 권한 폐기 시점 이후 실제 MediaMTX handshake/인증/취소 상한과 새 전체 조회를 결합하고, 이미 인가된 뒤 늦게 나타나는 연결의 kick을 검증한다.

## 04-B2 구현 인계 계약

04-B1의 인증·정상 미디어·장애 시 권한 차단 인터페이스 검증 후 시작한다. unknown POST 자동 회수는 04-B/07에서 미완료로 추적하며 완료로 간주하지 않는다. 다음은 현재 Flutter/실험 코드를 읽은 독립 설계 검토를 반영한 계약이며 새 제품 구현을 병렬로 시작하지 않는다.

- 화면은 미디어 조정자에 시작/종료를 요청하고 상태만 표시한다. 브라우저 송출 모듈이 capture·WebAudio·PC·미리보기를, 재생 모듈이 hls.js·video·리스너/타이머를 소유한다. 인터페이스의 목적은 자원 소유와 테스트 경계를 분명히 하는 것이며 불필요한 추상 계층은 만들지 않는다.
- 시작 클릭에서 첫 await 또는 API/마이크 요청 전에 getDisplayMedia를 호출한다. bridge는 클릭 전에 준비한다. 원본 display 트랙을 얻는 즉시 정리 함수와 ended 리스너를 등록한다. 마이크 확보·혼합 성공 후 회차를 생성한다. 권한 취소로 서버 회차를 만들지 않는다. H264 사전 검사·선호 코덱 설정·실제 협상 확인은 현재 app.js에 없으므로 별도 구현/검증한다.
- 기존 prepareSession은 session만 반환하며 grant를 버린다. 준비 응답을 session+publishGrant의 비공개 타입으로 받아 grant를 메모리에만 유지하도록 확장한다. 새 발급 endpoint를 임의로 만들지 않고 기존 생성/renew 계약을 따른다. 공개 LiveSession 모델에 grant를 넣지 않는다.
- JSON 요청과 SDP/ICE 요청을 구분한다. WHIP POST는 publish grant, PATCH/DELETE는 현재 access JWT를 사용한다. access token을 UI/전역 JS에 노출하는 getter 대신 인증 transport 경계로 처리한다. 동일 출처·해당 회차 Location과 ETag를 검증한다. 결과 불명 POST를 자동 재전송하지 않는다.
- 각 시도는 attempt/auth epoch와 정확한 회차/자원을 소유한다. 모든 await 뒤 유효성을 검사한다. 늦은 capture는 즉시 정리, 늦은 회차 응답은 그 회차만 stop, 늦은 WHIP201은 그 resource만 정리한다. 이전 작업이 새 회차/플레이어를 정리할 수 없어야 한다.
- 종료는 먼저 시도를 무효화하고 로컬 트랙·PC를 즉시 닫고 AudioContext 종료를 추적한다. 네트워크 종료 실패는 성공으로 표시하지 않는다. 화면 dispose 이후에도 서버 정리를 이어갈 수 있는 소유권을 둔다. 탭 종료 요청 전달은 보장하지 않으며 서버 감시를 최종 정리 근거로 사용한다.
- 로그아웃 요청 시작 때 로컬 송출을 중단한다. 로그아웃 API 실패 시 기존 로그인 상태는 유지하되 캡처를 자동 재개하지 않는다. 인증 만료도 같은 정리 경로를 사용한다. 늦은 refresh 응답으로 폐기한 인증이 복원되지 않도록 기존 인증 코드의 epoch 경계를 검증한다.
- 공유 음원 누락 안내, 마이크 실패 시 전체 정리, 두 입력 gain0.5, muted/playsinline 미리보기, AudioContext running/재개 안내를 제공한다. 실제 사용자 pause는 재시도·poll·fallback이 해제하지 않는다. 서버 LIVE와 playback 응답 전 재생 비활성화, 회차 변경/종료 시 이전 플레이어 완전 정리를 유지한다.
- Flutter 동작 검사는 권한 취소/부분 실패/중복 클릭/늦은 응답/화면 이탈/로그아웃/회차 변경/pause 유지를 포함한다. 실제 독립 Chrome 프로세스로 합성 영상 decoded frames와 두 음원 수신, 원본 트랙·PC·AudioContext 및 ingress/FFmpeg 종료를 검사한다. 실제 화면 선택·마이크 청취·두 물리 기기·외부망 인수는 별도다.

## 서버 체크포인트와 다음 구현

04-B1 정상 미디어·권한·종료·장애 경계를 검증했고 두 축 정적 리뷰 잔여0건이다. 조정자 실제 worker SIGKILL·자동 재시작·새 시청자33프레임·최종자원회수 및 pending LL-HLS 요청 종료(약2.1초404)를 독립 확인했다. 서버 인터페이스를 기반으로04-B2 Flutter 통합을 진행한다. 주소 없는unknown POST 자동회수는04-B/07에 남기며 부모04-B는in-progress를유지한다.

## 04-B2 로컬 검증 결과

Flutter PC 송출·익명 시청의 독립 analyze0·Flutter53개·JS3개·릴리스 빌드·실제 제품 UI 두 Chrome 검증과 두 축 리뷰를 통과했다. docs/verification/04b2-flutter-live.md를 따른다. 부모04-B는 unknown POST 자동회수가 남아in-progress를 유지한다.05-A는 검증한 정상·권한·종료 인터페이스를 사용해 로컬 구현을 시작할 수 있다. 실제 기기 관문을 완료한 것으로 해석하지 않는다.

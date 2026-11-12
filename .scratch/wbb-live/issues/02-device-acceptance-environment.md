# 02 후속: 인증된 HTTPS 실기기 검증 환경

**What to build:** 티켓02의 PC·휴대폰 검증을 위해 동일 출처 HTTPS로 접근 가능한 실험 구성을 준비한다. 현재 localhost 전용 미디어 실험의 보호를 유지하면서, 별도 검증 환경에서만 지정한 미디어 경로와 ICE 후보를 사용한다.

**Blocked by:** 02 CPU/VAAPI 구현 체크포인트(충족). 부모02의 실제 기기 관문을 수행하기 위한 준비이며 03/04를 시작하는 작업이 아니다.

**Status:** in-review

## 기준과 범위

- 기준 제품은226fefd, VAAPI는0f9d8d0이다. 기존 로컬 CPU83항목과 GPU13항목 성공은 합성 검증이며 실기기 증거가 아니다.
- `experiments/media-device-acceptance/`에 격리 실행 구성, 환경변수 예시, 기동 전 검사·종료 절차·검증 도구를 둔다. 공통 화면의 필요한 변경은 `experiments/media-single-rendition/`에서 수행한다. Flutter/제품 인증/API, ABR, Kafka는 범위 밖이다.
- 기존 helper의 loopback 강제와 기존 CPU/VAAPI 기본 Compose는 유지한다. 기존 서비스·DNS·Caddy·방화벽 설정을 직접 바꾸거나 검증 환경을 외부에 기동하지 않는다. 운영 변경안은 조정자에게 반환한다.
- 설계 조사상 live 도메인의 DNS는 응답하지만 기존 Caddy의 host matcher에는 없다. 실제 공개 접근·유효 인증서는 검증하지 않았다.

## 구현 계약

1. **브라우저 주소:** 비밀 없는 런타임 설정으로 WHIP/HLS 경로를 전달한다. 검증 HTTPS 모드는 동일 출처의 허용된 상대 경로만 사용하고, 외부 origin·scheme-relative URL·경로 탈출·localhost 폴백을 거절한다. 기본 localhost 모드는 기존 자동 검사를 보존한다. 쿼리로 임의 upstream을 지정하지 않는다.
2. **검증 접근 인증:** Caddy Basic Auth를 화면·자산·WHIP POST/PATCH/DELETE·HLS playlist/part/segment 전체에 적용한다. 자격은 비공개 파일/환경에서만 주입하며 예시에 실제 값은 넣지 않는다. 사용자명·비밀번호 해시·HTTPS host·ICE host/포트 등 필수 값이 없거나 잘못되면 기동하지 않는다. 임시 공유 검증 계정이며 제품 사용자 인증/소유자 권한의 완료 근거가 아니다. native HLS는 JS Authorization 주입을 전제로 하지 않는다. 브라우저 인증 challenge 이후 같은 출처 native 미디어 요청의 자격 유지 여부를 실제 기기에서 검증한다.
3. **경로와 프록시:** 기본 제안 경로는 `https://live.wombatburrow.com/__media-check/`다. 요청 경로와 응답 WHIP Location을 함께 변환하고 ETag/If-Match/Content-Type 및 PATCH/DELETE를 보존한다. Location은 같은 출처의 허용 WHIP resource 경로 안에서만 사용한다. HLS 상대 playlist/part/segment URI와 LL-HLS query를 유지한다. 임의 upstream·관리 API 경로를 공개하지 않는다.
4. **브라우저 요청 검증:** WHIP 변경 요청은 지정한 HTTPS Origin을 검사한다. 잘못된 Origin 및 누락 Origin의 브라우저 변경 요청을 거절한다. Authorization·쿠키·SDP·토큰·실제 자격·내부 주소를 공유 로그에 남기지 않는다. HLS native 재생은 Origin이 없는 GET을 별도로 고려한다.
5. **네트워크 격리:** 관리 API·RTSP·직접 WHIP/HLS HTTP 포트는 loopback 또는 격리 내부 네트워크만 사용한다. ICE만 명시한 검증 인터페이스·전용 포트에 바인딩한다. LAN 후보와 외부 후보는 환경에 따라 명시하며 자동 전체 인터페이스 공개나 방화벽 변경을 하지 않는다. LAN 성공을 외부 NAT/TURN 성공으로 기록하지 않는다.
6. **자원 수명:** 전용 Compose project·충돌 검사·제한 시간·실패 시 이번 실행의 자원 정리를 제공한다. 다른 WBB 실험과 기존 서비스를 종료하거나 같은 source에 동시에 송출하지 않는다. VAAPI 인코딩 실패 시 CPU 자동 대체는 하지 않는다.

## 완료 기준과 검증

- [ ] 실제 Caddy 설정을 adapt/validate하고 loopback 격리 실행의 HTTP 경계에서 미인증/잘못된 인증이 UI·WHIP 모든 메서드·HLS playlist/part/segment에서 거절되는지 검사한다. 경로 정규화/우회와 다른 Origin도 검증한다. mock proxy만으로 통과하지 않는다.
- [ ] 인증된 WHIP POST→Location→PATCH→DELETE, 잘못된 Location 거절, HLS 하위 리소스 및 blocking reload query 전달을 검사한다. 프록시 mock 검사는 실제 MediaMTX 검사와 구분한다.
- [ ] HTTPS 모드에서 혼합 콘텐츠나 모바일의 localhost 연결이 발생하지 않는다. 기존 CPU83 회귀 결과와 변경된 브라우저 동작 검사를 보고한다.
- [ ] 환경 누락/잘못된 주소/포트 충돌/기동 중 실패/종료 신호는 후속 기동 없이 실패하고 이번 자원만 정리한다.
- [ ] 검토 가능한 Caddy 추가 설정안, 실제 접근 인증값을 저장소 밖에서 준비하는 방법, 설정 검증·롤백 절차를 작성한다. 외부 적용은 아직 하지 않는다.
- [ ] 실제 PC→휴대폰, 휴대폰→PC, 두 음원·영상·종료·권한 거부 결과를 날짜·기기·OS·브라우저·망과 함께 기록한다. 사용자 캡처 허용·청취 확인 전에는 미검증이다.

구현·자동 검증이 끝나면 in-review로 전환한다. 실제 장치 접근 설정 및 수동 인수 전에는 이 티켓과 부모02를 완료로 처리하지 않는다. 인증서 경고를 우회하거나 무인증 서비스를 노출해 관문을 통과시키지 않는다.

## 현재 진행

2026-11-09 사용자가 개발 재개 및 지금 실기기 테스트 가능을 알렸다. 로컬 CPU helper8090과 기존 미디어 컨테이너는 가동 중이며 송출은 비활성이다. Chrome에 검증 화면을 열었다. 지정 pi의 미전송 `/u`는 보존했으며, 이후 입력창이 비워진 것을 실제로 확인하고 상태 확인만 전달했다. 정상 응답 후 같은 pi에 이 티켓의 구현을 전달했고 문서 읽기 및 구현 설계 작업 시작을 확인했다. 휴대폰 OS/브라우저와 실제 입력·청취 결과는 확인 중이다.

구현 중 pi가429/1310 주·월 한도와3회 재시도 종료를 반환했다. 사용자 승인에 따라 미커밋 변경 및 중간 리뷰를 지정 대체 Codex로 인계했고 실제 작업 시작을 확인했다. 현재 구현 담당은 대체 Codex이며 pi는 편집 중단 상태다. 조정자의 실제 Caddy 요청 검사와 두 축 중간 리뷰 실패는 `docs/reviews/02-device-acceptance.md`에 기록했다. 아직 통과/배포/실기기 성공으로 처리하지 않는다.

## 실제 기기·외부망 검증 진행 승인

2026-11-09 사용자가 휴대폰 연결과 외부망 검증을 함께 진행하도록 요청했다. 대상은 아이폰 Google Chrome이다. 먼저 검토된 격리 환경과 서버 접근 경로를 준비하고, Wi-Fi에서 PC→아이폰 및 아이폰→PC, 이동통신망에서 동일 흐름을 확인한다. 실제 캡처 권한과 영상·음성 청취는 사용자가 수행하고 결과를 확인한다.

기존 루트 도메인·서비스·광범위한 ACL은 보존한다. live 전용 DNS only·공개 인증서·인증된 경로·명시 ICE 포트의 최소 추가분만 대상으로 한다. 현재 live DNS는 Cloudflare 주소이며 HTTPS525, sudo 비대화형 방화벽 조회는 권한 부족이다. DNS 레코드 및 외부 포트 경로는 조사 중이며 임의로 해결됐다고 가정하지 않는다. 구체적 적용 범위와 롤백을 기록하고 필요한 사용자 관리 작업을 안내한다.

## 자동 검증 체크포인트

대체 Codex는 최종 수정·검사를 보고하고 편집을 중단했다. 명세/표준 잔여 확정 결함0건이며 조정자 독립 selftest20, 실제 HTTP16, 직접 Chrome14검사가 통과했다. 송출 중 FFmpeg PID의 종료와 기존 컨테이너 보존도 확인했다. 전체 CPU83 재실행, 실제 아이폰/외부망 및 일부 HTTP/query 세부 행렬은 아직 미완료다. 현재 상태는 in-review이며 위 미완료 항목을 완료로 표시하지 않는다.

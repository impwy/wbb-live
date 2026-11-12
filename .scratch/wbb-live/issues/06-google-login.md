# 06: Google 로그인으로 내 방송 관리

**What to build:** 사용자가 Google 로그인으로 자신의 방송을 만들고 로그인 상태 복원·로그아웃까지 이용한다.

**Blocked by:** 01 이메일 인증·방송 관리.

**Status:** in-review

**실행 가능 여부:** 선행01 완료로 진행 가능. 티켓02의 실제 기기 검증과 독립적으로 구현할 수 있다. 실제 Google 설정이 없으면 통합 완료가 아닌 외부 설정 미완료로 기록한다.

- [x] Code+PKCE/state/nonce, 정확한 callback, issuer/audience/expiry/subject 검증을 적용한다.
- [x] 서버에서 토큰을 교환하고 URL에 자체 access token을 넣지 않는다. callback query와 자격이 로그에 남지 않는다.
- [x] 같은 이메일의 기존 계정과 자동 연결하지 않고 충돌을 사용자에게 안내한다.
- [x] Flutter Google 버튼→callback→인증 복원→방송 생성→로그아웃이 연결된다.
- [x] 변조 state/nonce/issuer/audience, 중복 callback, 외부 redirect 입력을 거절하는 API 검사와 실제 Google 로그인 결과를 구분한다.

**상세 계약:** `docs/google-login-contract.md`. 브라우저 결합·일회성 callback·identity 동시성·Flutter 직접 완료 경로와 기존 nullable password_hash 회귀를 포함한다.

**검증:** AUTH-01~03. 테스트 provider 성공을 실제 Google 성공이라고 하지 않는다.

**결과:** 최종 스냅샷f1ba363 자동 검증과 두 축 리뷰 통과. 실제PG/Redis 공개HTTP 전체race·vet·호출경로취약점검사, callback/azp 독립red→green, Flutter공식27+독립9=36개, analyze·release build를 확인했다. [검증](../../../docs/verification/06-google-login.md)과 [리뷰](../../../docs/reviews/06-google-login.md)를 따른다.

**남은 인수:** 실제 Google Console 자격으로 동일 출처 브라우저에서 로그인·취소·충돌·로그아웃 확인. 테스트 provider 성공과 구분하며 이 관문 전에는 done으로 바꾸지 않는다. 23:45 KST pi 정상 응답과 대체 Codex 편집 중단 확인 후 지정 pi가 체크포인트를 인수했다. 추가 구현 없이 실제 인수 관문 대기이며 추가 한도 재확인 예약은 없다.

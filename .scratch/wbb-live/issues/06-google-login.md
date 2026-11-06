# 06: Google 로그인으로 내 방송 관리

**What to build:** 사용자가 Google 로그인으로 자신의 방송을 만들고 로그인 상태 복원·로그아웃까지 이용한다.

**Blocked by:** 01 이메일 인증·방송 관리.

**Status:** ready-for-agent

**실행 가능 여부:** 선행 미완료로 blocked. 실제 Google 설정이 없으면 통합 완료가 아닌 외부 설정 미완료로 기록한다.

- [ ] Code+PKCE/state/nonce, 정확한 callback, issuer/audience/expiry/subject 검증을 적용한다.
- [ ] 서버에서 토큰을 교환하고 URL에 자체 access token을 넣지 않는다. callback query와 자격이 로그에 남지 않는다.
- [ ] 같은 이메일의 기존 계정과 자동 연결하지 않고 충돌을 사용자에게 안내한다.
- [ ] Flutter Google 버튼→callback→인증 복원→방송 생성→로그아웃이 연결된다.
- [ ] 변조 state/nonce/issuer/audience, 중복 callback, 외부 redirect 입력을 거절하는 API 검사와 실제 Google 로그인 결과를 구분한다.

**검증:** AUTH-01~03. 테스트 provider 성공을 실제 Google 성공이라고 하지 않는다.

**결과:** 아직 미실행.

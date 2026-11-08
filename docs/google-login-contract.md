# Google 로그인 구현 계약 (티켓06)

상태: 설계, 아직 구현·실제 Google 로그인 증거가 아니다. 티켓01 완료로06은02의 실제 기기 관문과 독립 진행할 수 있다. 기존 이메일 로그인·방송 권한·refresh family 계약을 유지한다. 구현은 지정 pi GLM, 검토·독립 검증은 Codex다.

## 신뢰 대상과 설정

- 운영 provider는 Google 하나다. 클라이언트 요청·환경변수로 임의 issuer/token/JWKS endpoint를 신뢰하는 기능을 추가하지 않는다. 공식 Google discovery 또는 고정 공식 endpoint와 RS256 검증 라이브러리를 사용한다.
- 서버 환경변수는 Google client ID, client secret, 정확한 callback URI다. 전체 누락은 기능 비활성화로 기존 이메일 기능을 유지하고 start는503을 반환한다. 일부만 설정되거나 잘못된 URI이면 서버 시작 시 명확히 실패한다. 비밀 값은 문서·로그에 쓰지 않는다.
- callback은 정확히 설정된 origin의 `/api/v1/auth/google/callback`이다. HTTPS를 요구하고 개발 HTTP는 명시적 loopback만 허용한다. Host/X-Forwarded-Host/next/redirect_uri로 callback을 만들지 않는다.
- 공개 `GET /api/v1/auth/providers`는 `{googleEnabled: boolean}`만 반환한다. Flutter는 이 값이 true일 때 Google 버튼을 활성화하며 client ID/secret·외부 endpoint를 받을 필요가 없다.
- start/callback은 요청 제한을 적용하고 `Cache-Control: no-store`, `Referrer-Policy: no-referrer`를 설정한다. provider 원문 오류나 callback query는 로깅하지 않는다.

## 시작·브라우저 결합·일회성 callback

1. `GET /api/v1/auth/google/start`는 각각 독립적인 state·nonce·PKCE verifier·browser binding을 암호학적 난수로 생성한다. Google에는 scope `openid email profile`, response_type=code, PKCE S256, 고정 callback을 전달한다. 오프라인 provider refresh token은 요청·저장하지 않는다.
2. Redis의 state 해시 키에 binding 해시·nonce·verifier·만료를5분 TTL로 저장한다. browser binding은 host-only `HttpOnly; Secure(prod); SameSite=Lax; Path=/api/v1/auth/google` 쿠키로 전달한다. 쿠키만으로 세션을 만들지 않는다.
3. callback은 단일 state, 단일 code 또는 허용된 오류 입력을 검사한다. 중복 query, code와 error 동시 존재, 누락·만료·다른 브라우저의 binding을 거절한다.
4. binding 일치 확인과 state 소비를 Redis의 원자 연산으로 수행한다. 다른 브라우저의 잘못된 요청은 정상 state를 삭제하지 않는다. 올바른 요청은 **토큰 교환 전** 소비하여 순차·동시 재사용을 막는다. 교환 실패 후에도 재사용하지 않는다.
5. 브라우저당 최신 시작 하나만 지원해도 된다. 이전 시작·취소·만료는 새 로그인 안내로 마무리한다. Redis 장애는503이며 자체 인증 쿠키/세션을 발급하지 않는다.

## ID token과 사용자 연결

- 서명·RS256·issuer·audience·expiry·nonce·비어 있지 않은 subject를 검증한다. issuer는 Google이 문서화한 `https://accounts.google.com`과 `accounts.google.com`만 허용하고 DB에는 전자로 정규화한다.
- audience는 설정된 단일 web client ID여야 한다. azp가 있으면 같은 client ID여야 한다. 여러 audience를 느슨하게 허용하지 않는다. email_verified=true와 유효 이메일을 요구한다.
- 새 마이그레이션으로 `auth_identities(user_id, provider, issuer, subject)`와 `(provider,issuer,subject)` 유일 제약을 추가한다. password_hash는 NULL이다.
- identity를 이메일보다 먼저 조회한다. 기존 subject는 provider 이메일이 바뀌어도 같은 WBB user ID로 로그인한다. 이번 범위에서는 기존 이메일·표시 이름을 자동 동기화하지 않는다.
- identity가 없을 때만 정규화 이메일 충돌을 확인한다. 동일 이메일의 로컬 계정이나 다른 subject는 자동 연결하지 않고 account_exists로 안내한다. 새 user와 identity는 한 DB 트랜잭션으로 만든다.
- 동일 subject의 동시 첫 로그인 경쟁은 롤백 후 승자의 identity를 확인하여 같은 사용자로 귀결시킨다. 다른 identity의 동일 이메일 충돌은 거절한다.
- 최초 표시 이름은 trim 후 최대40 rune으로 제한하고, 비어 있으면 `Google 사용자`를 쓴다. 자체 session family 생성은 기존 auth 흐름을 재사용한다.
- 기존 Login의 nullable password_hash를 string으로 Scan하는 부분을 수정한다. Google 전용 계정의 비밀번호 로그인은500이 아닌 일반401이다.

## HTTP와 Flutter 완료 흐름

- 성공 callback은 기존 refresh 쿠키를 설정하고 고정 경로 `/auth/google/complete`로303한다. 자체 access JWT·Google token·code·state는 URL에 넣지 않는다.
- 예상 가능한 취소·실패도 같은 경로에 유한한 오류 코드만 전달한다: cancelled, invalid_request, account_exists, unavailable. provider error_description·이메일·임의 redirect 입력을 전달하지 않는다. Redis 장애는 앞 절의503 직접 응답을 우선하고 unavailable 리다이렉트는 그 외 예상 가능한 provider 장애에 사용한다. 외부 redirect를 start에 주입하면400으로 거절한다.
- Flutter 버튼은 start로 top-level navigation한다. callback 경로 직접 진입과 새로고침을 지원한다. 기존 앱의 auth.restore 결과를 공유하고 callback 화면에서 별도 refresh를 중복 호출하지 않는다.
- 성공 시 방송 관리 화면, 실패 시 이해 가능한 오류와 로그인 재시도 경로를 제공한다. 오류 없는 callback 주소만 입력해 인증에 성공한 것처럼 표시하지 않는다. access token은 계속 메모리에만 둔다.

## 검증

실제 PostgreSQL·Redis와 공개 HTTP를 사용한다. 테스트 provider는 code 교환, client 인증, 정확한 callback과 S256 verifier를 검사하며 RSA 서명 ID token/JWKS를 제공한다. 고정 Google URL의 네트워크 요청을 테스트 transport로 주입하고 운영의 임의 issuer 설정·서명 생략 옵션을 만들지 않는다.

필수 시나리오:
- 정상 로그인→refresh→me→방송 생성→로그아웃, identity 재로그인, 이메일 변경 후 같은 사용자.
- 동일 이메일의 로컬 계정/다른 subject 충돌, 동시 동일 identity 생성, OIDC 계정의 비밀번호 로그인401.
- 다른 브라우저·누락/변조/만료 state, 중복/동시 callback, nonce/issuer/audience/azp/서명/expiry/subject/email_verified 오류.
- code 교환 실패 후 재사용 거절, Redis 장애, 외부 redirect·중복 query·provider 오류 원문 차단.
- callback 응답의 Location·본문 및 모든 애플리케이션 로그에 code/state/token/secret 없는지 검사한다. 의도된 HttpOnly refresh 쿠키와 기존 refresh API의 accessToken 응답은 예외다. Google 승인 요청에 필수인 state·nonce·PKCE challenge 전달은 이 제한의 대상이 아니다. Flutter 버튼·직접 callback·복원·취소·충돌·로그아웃 사용자 동작.

실제 Google Console 자격을 이용한 브라우저 로그인은 별도 관문이다. 테스트 provider 성공을 실제 Google 성공이라고 표시하지 않는다.

## 근거

- [Google OIDC](https://developers.google.com/identity/openid-connect/openid-connect): issuer·ID token 검증·subject 식별.
- [Google discovery](https://accounts.google.com/.well-known/openid-configuration): 운영 endpoint와 지원 알고리즘.
- [OAuth 보안 BCP](https://www.rfc-editor.org/rfc/rfc9700.html#section-4.7.1): 브라우저 트랜잭션 결합과 CSRF 방어.
- [OIDC ID token 검증](https://openid.net/specs/openid-connect-core-1_0.html#IDTokenValidation): audience·azp·nonce 검증.

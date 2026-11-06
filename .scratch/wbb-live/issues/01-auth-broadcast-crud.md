# 01: 이메일 인증과 내 방송 관리

**What to build:** 방문자가 공개 목록을 열고, 가입·로그인 후 방송을 등록·수정·삭제하며 새로고침과 로그아웃의 결과를 웹에서 확인한다. 실제 PostgreSQL을 연결한 첫 수직 기능이다.

**Blocked by:** None (can start immediately).

**Status:** done

**구현 담당:** 지정 pi GLM. Codex 리뷰 전 done 금지.

**계약:** register201 {user}(자동 로그인 없음), login/refresh200 {accessToken,user}, me200 {user}, logout204. 외부 JSON camelCase. access JWT audience=wbb-api. 방송 ownerId는 서버 결정, title1~100, description0~2000, version 조건부 수정. 초기 미송출 상태 READY. 개인정보는 공개 목록에 포함하지 않는다.

- [x] Flutter 공개 목록/빈 상태, 가입·로그인·로그아웃, 내 방송 등록·조회·수정·삭제 흐름이 API와 연결된다.
- [x] PostgreSQL 마이그레이션과 볼륨 보존 재시작 후 사용자·방송·소유권이 유지된다.
- [x] anonymous401/다른 사용자403/없는 방송404/버전 충돌409를 직접 HTTP 요청으로 확인한다.
- [x] access JWT10분, 회전 refresh7일, 운영 설정의 Secure/HttpOnly cookie와 Origin 검증을 적용한다. 비밀을 localStorage/로그에 남기지 않는다.
- [x] PostgreSQL auth_sessions와 family가 최종 권한 원장이다. 로그아웃 후 기존 access/refresh를 거절하고 refresh 재사용을 탐지한다.
- [x] Redis 장애 시 보호 API503, 익명 조회는 계속 동작한다. revoked session이 캐시 삭제 실패로 부활하지 않는다.
- [x] Go 검사, 실제 DB API 통합 테스트, Flutter analyze/test/web build를 실행하고 결과를 기록한다.

**검증:** AUTH-01~03, BCAST-01/04, BASE-01. 빈 목록과 두 계정 권한 거절을 재현한다. 미디어와 Google 로그인은 이 티켓의 완료 범위가 아니다.

**결과:** 지정 pi GLM 구현과 Codex 독립 리뷰·수정 검증 완료. 백엔드30개 테스트(-race), Flutter15개 테스트, 정적 검사·빌드, 로컬 Headless Chrome156 사용자 흐름, 실제 DB 컨테이너·API 프로세스 재시작 영속성 확인. 상세 근거는 `docs/verification/01-auth-crud.md`, 리뷰는 `docs/reviews/01-backend.md`와 `docs/reviews/01-frontend.md`를 참조한다. 운영 HTTPS 쿠키 전송·실제 휴대폰 검증과 공개 배포는09에서 수행한다. 미디어·Google·Kafka·LGTM은 이 완료 범위에 포함되지 않는다.

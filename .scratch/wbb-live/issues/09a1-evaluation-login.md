# 09-A1: 제출용 평가 로그인

**Status:** ready-for-agent
**Blocked by:** 07-A2 로컬 종료 계약(2026-11-11 충족). 공개 접속·실기기 인수는 부모 09-A에 남는다.
**목표:** 평가자가 비밀번호를 입력하지 않고 전용 평가 계정으로 로그인해 방송을 등록할 수 있도록 한다. 기존 가입·로그인·로그아웃, JWT·서버 세션, 소유자 인가를 재사용한다.

## 범위·인터페이스

- `WBB_EVAL_LOGIN_ENABLED`, `WBB_EVAL_LOGIN_EMAIL`, `WBB_EVAL_LOGIN_PASSWORD`를 모두 설정했을 때만 활성화한다. 기본 비활성, 부분 설정 또는 모순 설정은 기동 오류다. 전용 계정은 기존 가입 API로 미리 만든다. 암호는 Git에 없는 배포 Secret으로만 주입하고 응답·로그·문서에 포함하지 않는다.
- `POST /api/v1/auth/evaluation-login`은 요청 본문 없이 정확한 허용 Origin과 Redis의 별도 IP·전용 계정 제한을 통과한 후 기존 `auth.Login`으로 그 계정의 보통 access JWT, 인증 family/session, HttpOnly refresh cookie를 발급한다. 전용 계정이 없거나 암호가 맞지 않거나 Redis가 불가하면 세션을 발급하지 않는다. 비활성일 때는 숨겨진 버튼과 사용할 수 없는 서버 경로를 보장한다.
- `GET /api/v1/auth/providers`에 `evaluationEnabled`를 추가한다. Flutter 로그인 화면은 활성일 때만 버튼을 표시하고, 누르면 기존 AuthController의 로그인 상태 갱신·새로고침 복구·로그아웃 경로를 사용한다. 버튼 오류와 중복 클릭에는 세션 상태를 잘못 갱신하지 않는다. 기존 Google 버튼과 이메일 로그인을 유지한다.
- 일반 사용자 방송을 평가 계정이 수정·삭제·송출 제어할 수 없어야 한다. 반대로 평가 계정의 방송도 다른 사용자가 제어할 수 없다. 익명 조회·시청은 그대로다. 공개 전용 구성에는 환경변수 자리표시자만 남긴다.

## 완료 기준·검증

- config 단위 경계: 기본 비활성, 완전 활성, 부분·모순 설정 거절.
- 공개 HTTP+실제 PostgreSQL+Redis: 활성 성공/없는 계정/잘못된 암호/Origin 불일치/독립 IP·계정 제한/Redis 장애/refresh·logout/소유자·다른 사용자·익명 권한을 검증한다. 내부 인증 helper만으로 완료를 주장하지 않는다.
- Flutter: providers false/오류에서는 버튼 없음, true에서는 버튼과 정상 인증 상태, 실패 시 기존 사용자 상태 보존. `analyze`와 관련 widget 테스트·웹 빌드를 실행한다.
- 전용 로컬 스택에서 실제 로그인 버튼→방송 등록→로그아웃을 확인한다. 실제 공개 HTTPS에서 동일 버튼을 확인하기 전에는 부모 09-A의 평가 로그인 인수를 완료로 표시하지 않는다.

지정 구현자는 AGENTS.md와 `docs/agents/implementation-handoff.md`를 읽고 이 범위만 단독 편집한다. 변경 파일·실행 검사·미해결 점을 보고한 뒤 편집을 멈춘다. 조정자는 두 축 리뷰와 독립 검증을 수행한다.

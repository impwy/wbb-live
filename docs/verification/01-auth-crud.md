# 01 인증·방송 관리 검증

검증일: 2026-11-06. 구현: 지정 pi GLM. 설계·리뷰·독립 검증: Codex와 리뷰 서브에이전트.

## 환경과 범위

- macOS arm64, Go1.27.3, Flutter3.47.9/Dart3.13.7.
- Docker의 실제 PostgreSQL과 실행마다 분리한 테스트 Redis.
- 로컬 HTTP의 Headless Chrome156. 데스크톱과390px 뷰포트 확인.
- 이번 완료는 이메일 인증과 방송 메타데이터 CRUD다. 라이브 송출·시청·Google OAuth·Kafka·LGTM·실서비스 배포는 완료하지 않았다.

## 자동 검사

| 검사 | 결과 |
| --- | --- |
| `go test -race -count=1 ./...` | HTTP 통합 테스트30개 통과, 최종 실행 약42.5초 |
| `go vet ./...`, `go build ./cmd/wbb-api` | 통과 |
| `govulncheck ./...` | 호출 경로0건, import 패키지0건. 미사용 OpenPGP 모듈 경고1건은 백엔드 리뷰에 기록 |
| `flutter analyze` | 통과 |
| `flutter test` | 15개 통과 |
| `flutter build web --release --no-web-resources-cdn` | 통과 |

API 검사는 비로그인401·다른 소유자403·없는 방송404·version409, refresh 회전·동시 재사용, 로그아웃, 캐시 유실·복구·회전 경합, Redis 장애503와 익명 조회, 계정/IP 제한, 문자 수·입력 정규화, 페이지네이션을 포함한다.

공개 커밋 `cf058cf`의 [GitHub Actions 실행](https://github.com/impwy/wbb-live/actions/runs/42190069816)에서 backend와 frontend 작업이 모두 성공했다. 원격 CI에서도 Go 검사·취약점 검사 및 Flutter analyze/test/web build가 통과했다.

## 직접 수행한 흐름

1. 브라우저 가입→로그인→내 방송 등록→수정→새로고침→로그인 상태 복원→상세 조회→삭제→로그아웃.
2. 로그아웃 후 쿠키 제거 확인. localStorage와 sessionStorage에 항목 없음.
3. PostgreSQL 컨테이너를 볼륨 보존 상태로 재생성한 뒤 ID·ownerId·title·description·version·createdAt 재조회 일치.
4. API 프로세스 재시작 뒤 방송 정보 재조회 일치. 개발용 서명 키 변경으로 첫 보호 요청401→refresh200→원래 요청201 자동 복구 확인.
5. 내 방송에서 홈으로 복귀했을 때 새로 등록한 방송이 목록에 즉시 반영됨.
6. 새 브라우저 세션에서 CanvasKit·JS·폰트·API 요청이 모두 로컬 출처로 향함. 한글 폰트 CDN 요청 없음.

## 검증하지 않은 것

운영 HTTPS에서 Secure 쿠키 전송, 실제 두 탭 동시 refresh, 실제 휴대폰·카메라·마이크·PC 공유 오디오, 외부망/NAT/TURN, LL-HLS/ABR, 부하 용량, 운영 배포는 후속 티켓에서 검증한다. 같은 인스턴스의 refresh single-flight를 탭 간 동기화로 표현하지 않는다.

# 06 Google 로그인 리뷰

2026-11-08. 기준 `ff2d876`, 고정 초기 구현 스냅샷 `28ce073`. 지정 대체 Codex 구현을 새 독립 Standards/Spec 에이전트로 검토했다. 초기 지적과 수정 이력을 기록하며 최신 결론은 마지막 절을 따른다. 실제 Google 자격 로그인은 미검증이다.

## Standards

- **P2 S1 — 존재하는 잘못된 azp 허용:** `wbb-backend/internal/httpapi/google.go:270,276`. 문자열 helper가 미존재·빈 값·잘못된 타입을 같게 취급한다. 계약의 “azp가 있으면 같은 client ID”를 위반한다. 존재 시 문자열 타입과 정확한 client ID를 요구하고 빈 문자열/null/숫자/배열을 공개 HTTP signed-token 검사에 추가한다.
- **P2 S2 — callback 파싱 오류와 키 존재 무시:** `google.go:126–134`. URL.Query는 파싱 오류를 버리고 값 비교는 키 존재를 구분하지 못한다. `code=good-code&error=` 또는 `code=good-code&code=%ZZ`가 거절되지 않을 수 있다. ParseQuery 오류, 키 존재, 단일성, 비어 있지 않은 값을 분리해 검사한다. 보안 의미가 있는 state/code/error의 중복·동시 존재는 계속 거절한다.

코드 냄새 별도 지적 없음. Standards2건, 최고P2.

## Spec

- **P1 F1 — 실제 Google callback 부가 매개변수 거절:** `google.go:128`. state/code/error/error_description 외 모든 이름을 거절해 정상 scope/authuser/prompt/hd를 포함한 응답이 invalid_request가 된다. [Google 공식 code-model 문서](https://developers.google.com/identity/oauth2/web/guides/use-code-model?hl=en)의 응답 매개변수·예시 및 미인식 응답 매개변수 무시 지침을 따른다. 부가 값은 인증·redirect·계정 연결 판단에 쓰지 않는다. 중복·malformed 보안 입력 거절을 유지하며 실제 형태의 callback을 공개 HTTP 검사에 추가한다.
- **P2 F2 — 실패 후 이메일 로그인 완료 경로 단절:** `wbb-frontend/lib/screens/google_complete_screen.dart:47–48`. 직접 진입한 완료 화면을 LoginScreen으로 교체한 뒤 이메일 성공은 login_screen.dart:55의 pop만 호출한다. 마지막 route를 잃을 수 있다. 오류→재시도→이메일 성공→홈/방송 관리까지 사용자 동작으로 확인하고 경로를 수정한다. 기존 테스트는 재시도 화면 도착까지만 확인한다.
- **P2 F3 — azp 존재 검증 누락:** Standards S1과 동일 코드 결함으로, 명세의 존재 시 정확한 client ID 요구에도 위배된다.

Spec3건, 최고P1. 중복 지적을 제외한 수정 대상은4건이다. 원자적 state 소비·identity 우선 조회/경합·nullable 비밀번호·refresh 복원 공유는 코드상 확인했으나 실행 검증은 별도로 수행한다.

## 인계와 완료 기준

18:40 KST pi가 상태 확인 요청에 정상 응답했다. 대체 Codex는 “편집 중단, pi 인계 가능”을 응답했고 구현/검사 프로세스가 없다고 보고했다. 기존19개 변경을 보존하여 지정 pi로 복귀한다. pi는 위4건의 실패 재현→수정→관련 및 전체 검사 결과를 보고한다. 조정 Codex의 독립 실행·재리뷰 전 완료/승인으로 표시하지 않는다.


## 조정 Codex의 독립 재현

고정 스냅샷28ce073의 별도 임시 복사본에서 원래 제품 코드는 유지하고 검증 입력만 추가했다. 전용 실제 PostgreSQL·격리 Redis·공개 HTTP 검사에서 정상 Google 부가 query 거절, code+빈 error 허용, 잘못 인코딩된 중복 code 허용, azp의 빈 문자열/null/숫자/배열 허용을 모두 재현했다(기대 보안 조건7건 실패·전체exit1). 기존 악성 nonce/issuer/audience/서명/만료 등의 거절은 통과했다.

Flutter 별도 복사본의 기존21개 검사는 통과했다. 추가 사용자 동작 검사의 일반 홈→이메일 로그인→홈/내 방송은 통과했지만, callback 직접 진입의 account_exists/cancelled 두 경로는 이메일 로그인 성공 이후 Text 위젯이0개인 빈 화면으로 실패했다. 실제 Google 자격을 이용한 검사는 아니다. 원본 저장소의 제품코드/테스트는 이 독립 재현에서 수정하지 않았다.


## pi 수정 재검토 (5a5776a)

초기4개 결함은 코드상 해소됐다. 동일한 공개HTTP 독립 재현은 정상 부가query 성공과 malformed query/azp6건 거절로 모두 GREEN이다. 두 축 재리뷰에서 같은 **P2 잔여1건**을 확인했다: callback 실패→재시도→가입하기→가입 성공 시 `signup_screen.dart:43–45`의 pop 두 번이 유일 LoginScreen까지 제거한다. 이메일 성공에만 추가한 목적지 전달을 가입 성공에도 일관되게 적용하거나 정상 홈 route를 유지해야 한다. 가입·로그인 두 경로의 정상 진입과 callback 재시도 진입을 모두 사용자 동작으로 검증한다.

Standards 잔여1건·최고P2, Spec 잔여1건·최고P2(동일 결함). 아직 승인하지 않는다.


## 가입 수정 재검토 (fcdf59b)

공식25개와 독립 이메일/가입6개는 통과해 빈 화면은 해소됐다. 다만 **P2 완료된 Login route 잔존**이 남았다. `[Login, Signup]`에서 공유 콜백의 pushReplacement(Home)은 Signup만 교체하여 `[Login, Home, MyBroadcasts]`가 된다. 독립 검사에서 두 callback오류 경로 모두 내 방송→뒤로홈의 canPop=true였고, 실제 platform Back 후 로그인 폼이 다시 표시됐다. 일반 홈 가입 control은 canPop=false·Back 후 홈 유지였다(독립3건 중2실패).

성공 시 홈을 새 root로 정해 완료된 인증 경로 전체를 제거한다. callback 이메일/가입 모두 같은 완료 동작을 사용하며, Home으로 돌아온 뒤 Back/숨겨진 LoginScreen까지 검증한다. Standards/Spec 모두 같은P2 1건이며 승인 대기다.


## 최종 검토·자동 검증

최종 스냅샷 `f1ba363`은 완료된 인증 화면을 pushAndRemoveUntil로 함께 제거한다. `2a40752`의 두 축 최종 코드 리뷰는 **Standards 잔여0건, Spec 잔여0건**이다. 이후f1ba363은 공식 테스트 중괄호 lint 정리만 포함하며 제품 코드 변화는 없다.

최종 고정 코드의 독립 Flutter 공식27+추가 사용자동작9=36개, analyze가 통과했다. 같은 제품 코드의 release web build도 통과했다. 두 오류 유형의 이메일/가입·일반홈control과 실제 platform Back에서 홈 유지/숨은 인증화면 없음까지 확인했다. backend는 독립 전체vet/race/호출경로 취약점 검사와 최초 실패 입력의 동일 red→green 재검증을 통과했고 최종 스냅샷에서도 변경되지 않았다.

자동 검사 범위는 승인한다. 실제 Google Console 자격으로 동일 출처의 브라우저에서 로그인하는 외부 인수는 아직 수행하지 않았으므로 티켓06은 in-review로 유지한다. 전체 서비스 배포·실제 기기·미디어 관문 완료를 뜻하지 않는다.

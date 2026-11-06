# Matt Pocock 스킬 적용

참고: https://www.slog.gg/p/14929 (2026-11-06 확인), 원본 스킬: https://github.com/mattpocock/skills.
설치 기준 커밋: `4ae2317ddb22b046e26455e4f9c9b3aaaf62212d`.

튜토리얼의 질문 → 명세 → 티켓 → 구현·리뷰 흐름을 사용한다. 예제의 Dioxus·Sonnet·인증 제외는 WBB 요구사항이 아니다.
설치한 버전은 튜토리얼의 CONTEXT.md 명칭 대신 GLOSSARY.md를 사용한다.

1. 설계 재검토에는 `grill-with-docs`를 사용한다. 질문만 필요하면 `grill-me`를 명시적으로 호출한다. 이미 답한 기술·범위 결정은 다시 묻지 않는다.
2. `to-spec`으로 확정 내용을 명세화하고, `to-tickets`의 방식으로 동작을 확인할 수 있는 작은 수직 작업으로 나눈다.
3. 구현은 pi GLM이 `tdd`와 `codebase-design`을 사용한다. 문서·API 계약을 먼저 읽고 테스트 실패→구현→통과를 기록한다.
4. Codex는 `code-review`의 명세·표준 두 축을 독립 서브에이전트로 검사한다. 수정은 pi에 반환한다.
5. 재현이 어려운 실패에는 `diagnosing-bugs`를 사용한다. 실제 디바이스 검사와 모의 테스트 결과를 구분한다.

원본 `implement-spec`의 자동 구현자 배치는 현재 사용하지 않는다. 동일한 티켓 진행 원칙을 지정된 pi 구현 세션에 적용한다.
사용자 승인 없이 구현 모델을 Sonnet이나 Codex로 바꾸지 않는다.
스킬의 문서·티켓 검토 절차는 사용자 승인 범위 안에서 진행한다. 불명확한 제품 결정만 질문한다.
`setup-matt-pocock-skills`, `grill-with-docs`, `to-spec`, `to-tickets`, `grill-me`는 명시 호출 전용이다. pi에서는 `/skill:<name>`으로 호출한다.

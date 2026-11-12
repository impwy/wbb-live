# 작업 관리: 로컬 Markdown

- 명세: `.scratch/<feature>/spec.md`.
- 티켓: `.scratch/<feature>/issues/<NN>-<slug>.md`, 티켓마다 파일 하나.
- `Status`: `needs-info`, `ready-for-agent`, `in-progress`, `in-review`, `done` 중 하나.
- `Blocked by`에는 실제 선행 티켓 번호를 적는다. 선행 완료 전에는 시작하지 않는다.
- 티켓에는 외부에서 확인 가능한 완료 기준과 실행한 검증 결과를 기록한다. 검증되지 않은 항목은 미완료로 남긴다.
- 스킬의 “publish to issue tracker”는 여기의 로컬 파일 저장을 뜻한다. GitHub Issues를 자동으로 만들지 않는다.
- 조정 Codex가 명세·티켓·리뷰 결과를 관리한다. 제품 구현 담당과 한도·오류 시 전환은 AGENTS.md 및 `implementation-handoff.md`를 따른다.
- 완료 변경은 공개 `wbb-live` 저장소에 게시할 수 있다. 공개 전 비밀과 사용자 로컬 경로를 검사한다.

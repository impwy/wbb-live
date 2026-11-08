# 02 VAAPI 후속 리뷰

2026-11-08. 기준 `813e0b9`, 고정 검토 스냅샷 `ba2050f`. 구현은 지정 pi GLM, Standards/Spec는 새 독립 에이전트, 실행 검증은 Codex다. 현재 **수정 대기·미승인**이다. GLM이429/1308을 반환하여 Codex가 구현을 대신 수정하지 않았다.

## Standards

- **P2 — 중단 신호 처리 후 기동 계속:** `experiments/media-vaapi/start.sh:34`의 `trap cleanup EXIT INT TERM`은 INT/TERM에서 종료하지 않는다. build 중 신호 후 자식이 반환하면 정리 뒤 다음 compose up으로 컨테이너를 다시 만들 수 있다. 티켓의 초기화 실패 정리 계약 위반이다. EXIT 정리와 INT/TERM의 비정상 종료를 분리해야 한다.
- **P2 — 모의 검사가 실제 종료 기준을 덮어씀:** `selftest-termination.py:164`와 verifier가 같은 `/tmp/wbb-media-vaapi-term` 파일을 쓴다. selftest가 실제 before를 삭제하고 가짜 PID·컨테이너·HTTP 주소를 남긴다. 상태 경로를 주입하고 selftest는 독립 임시 디렉터리를 써야 한다. AGENTS의 기존 변경 보존과 모의/실제 증거 분리 기준에 맞지 않는다.
- **P2 — 준비되지 않은 로컬 이미지 태그 의존:** `preflight.sh:12`는 digest FROM build 뒤 별도 `alpine:3.24` 태그를 inspect한다. 깨끗한 BuildKit 환경에서 태그가 없으면 GPU 검사 전 실패하고, 태그가 있더라도 실제 기반 digest와 다를 수 있다. 빌드의 고정 참조·메타데이터를 기록해야 한다.

별도 코드 냄새 지적은 없었다. Standards3건, 최고P2.

## Spec

- **P1 — 삭제된 종료 스크립트 호출:** `verify-browser-local.mjs:124,130`은 `sh ./verify-termination.sh`를 호출하지만 최종 파일은 `.py`다. 원래 encoder·source/output·part 중단을 검사한다는 계약이 깨끗한 복사에서 실행되지 않는다. 호출을 Python으로 연결하고 이전 파일이 없는 환경에서 다시 실행해야 한다. 원격 잔존 셸 파일을 사용한 pi13/13은 최종 코드의 전체 통과 근거가 아니다.
- **P2 — 음성 관측 생략을 전체 통과로 집계:** `verify-browser-local.mjs:109–110,137–139`는 audioLevel=-1이면 SKIP 로그만 출력하고 결과 배열에 기록하지 않는다. 나머지 성공 시 N/N·exit0이 가능하다. 해당 실행은 서버 PCM 검사도 호출하지 않는다. 필수 오디오 비영 관측 불가를 실패/미완료로 명시하거나 같은 방송의 검증된 대체 관측을 수행해야 한다.
- **P2 — SSH 준비·정리 절차 누락(문서 보완):** 스냅샷의 수동 안내가 없는 VAAPI README를 참조했다. Codex가 README에 깨끗한 복사, helper 재사용, 포워딩, 소유 자원 정리와 현재 제약을 작성했다. 코드 수정이 아닌 문서 보완이며 이 지적은 해결했다.

범위 확장은 없었다. Spec 최초3건 중 문서1건 해결, 미해결2건·최고P1. 실제 기기·외부망·Safari·ABR는 명시적 제외 범위다.

## 독립 재현과 수정 이력

- Codex가 이전 셸 함수에 Docker 조회 실패를 주입했을 때 convergence/관측 모두 성공으로 지나가던 문제를 재현했다. pi가 종료 검사를 Python으로 바꾸고 오류 주입 검사를 추가했다.
- 입력 환경변수 전달, 실제 입력 크기 확인, profile source 정리, 두 HTTP200/part 진행 확인 뒤 before 상태 게시, 원래 PID 직접 조회, 최소6초 이후 마지막 표본 검사는 예비 리뷰 후 수정했다.
- Codex는 새 원격 디렉터리로 고정 스냅샷을 복사하고 옛 `.sh`가 없음을 확인했다. 브라우저의 미디어 관측은 통과했지만 종료 스크립트 호출에서 원격127·전체exit1로 실패했다.
- Codex가 현재 start.sh의 외부 명령을 임시 stub으로 대체하고 build 중 SIGTERM을 전달했다. cleanup down 이후 up transcoder가 다시 호출되고 exit0이 되는 것을 재현했다.
- 별도 Codex 검증 명령으로 현재 Python before/after를 직접 호출했을 때 실제 GPU 브라우저 경로와6초 종료 관측은 통과했다. 이는 자동 검증기 연결 오류를 수정한 것으로 간주하지 않는다.

## 재개 시 수정 목록

1. 브라우저 verifier의 Python 호출·필수 음성 판정·낮은 수준 실패의 명확한 집계를 고친다. 깨끗한 원격 복사에서 전체를 다시 실행한다.
2. start 신호 종료, selftest 상태 격리, preflight 기반 이미지 기록을 고친다. 오류 주입은 compose ps 단계뿐 아니라 원래 PID 조회 단계의 실제 exec 실패까지 도달해야 한다.
3. pi가 생성한 package-lock은 Codex가 추적 대상으로 포함했다. `.gitignore`의 lock 제외 줄을 정리하고 `__pycache__`를 제외한다. 미디어 검사 subprocess/네트워크 대기를 제한하여 실패 시 무한 대기하지 않도록 검토한다.
4. pi 수정 후 Codex가 변경 범위 재검토·독립 실행한다. 실제 기기·외부망 인수와 티켓02 완료를 주장하지 않는다.

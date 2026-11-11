# 07-A2 제품 종료 완료 통합 검증

상태: 로컬 제품 통합 검증 중. 제품 기준 `b5b0ff7`와 그 뒤의 종료 경합 수정에서 전용 `wbb-media-completion-integration` 스택의 공개 HTTPS·실제 PostgreSQL·고정 패치 Linux MediaMTX/FFmpeg를 사용했다. 아래는 실제 실행한 범위와 아직 실행하지 않은 범위를 구분한다. 07-A1 격리 결과를 이 제품 통합의 성공으로 대체하지 않는다.

## 실행 결과 (2026-11-11)

- 조정자 독립 Go 전체 `go test -race ./...` 성공(7개 테스트 패키지), Flutter `analyze` 0건·59개 테스트·웹 release build 성공. 종료 경합 수정 후에도 조정자가 전용 PostgreSQL로 전체 Go race를 재실행해 성공하고 Flutter analyze·59개를 재실행했다. 로그는 `/tmp/wbb-07a2-root-go-race-final.log`, `/tmp/wbb-07a2-root-flutter-final.log`다. 첫 Go 재실행은 기본 포트 55432의 DB 미기동으로 실패해 전용 DB 55439를 명시하고 다시 통과했다. 구현자의 전체 race/vet/Flutter 검사도 통과했다.
- 조정자 독립 공개 HTTPS의 두 Chrome 프로세스에서 WHIP 201, H264 화면과 두 구분 가능한 음원, 익명 HLS 시청, Stop 202→ENDED, 저장된 `completed` 근거, FFmpeg·ingress 부재를 확인했다(`/tmp/wbb-07a2-root-browser.log`).
- 자연 FFmpeg 종료에서 종료 기록 뒤 세션 전이를 한 차례 거절한 경우와 종료 기록 자체를 한 차례 거절한 경우 모두 재시도해 `ENDED`가 됨을 조정자가 재검증했다(`/tmp/wbb-07a2-root-transition-retry.log`, `/tmp/wbb-07a2-root-exit-record-retry.log`). 두 경우 모두 저장된 `completed` 근거와 FFmpeg·ingress 부재를 확인했다.
- 구현자 검사에서 worker PID 강제 교체, Stop 중 worker 중지·lease 만료 후 재시작, PostgreSQL 중단·복구, MediaMTX 관리 API 일시 중단, POST 201 답변 유실, upstream 4xx, clean down/up과 새 이력 볼륨 bootstrap, URL 뒤 `/`가 있는 API/worker 기동을 통과했다. 두 방송 중 A 종료에도 B의 PID/epoch·영상·음성이 유지됐다. 구체 로그는 `/tmp/wbb-07a2-impl-*.log`와 `/tmp/wbb-07a2-spec-trailing-browser.log`다. 이는 구현자 검사로 표시하며 조정자 독립 검증으로 바꾸어 기록하지 않는다.
- 구형 0007 DB를 0008로 올리는 검사는 미귀속 과거 작업과 종료된 과거 회차를 새 완료 근거로 소급하지 않았고 자료를 보존했다.

남은 제품 관문: 등록·인증·AddPublisher·RTP barrier 전체와 MediaMTX A→B→C 교체 매트릭스, 구형 미완료 작업의 운영 복구 도구. 07-A1 격리 계약에서 수행한 검사를 제품 검사로 계산하지 않는다. 실제 iPhone·외부망·Google 자격·공개 평가 로그인은 09-A 인수에서 별도 판정한다.

| 검사 | 주입·관측 | 종료 조건 |
| --- | --- | --- |
| migration | 기존 방송/작업을 가진 DB에 새 migration 적용·API 재시작 | 구형 작업의 tuple/proof를 소급 채우지 않고 자료 보존 |
| 귀속과 dispatch | lifecycle 변경, Stop 선행/후행, API 중단을 commit 직전/직후에 주입 | 전송 전 tuple·SDP digest commit, never-sent는 DB 직렬화 증거, dispatch된 작업은 저장 epoch 유지 |
| 송출 응답 | upstream 4xx, POST 성공 답변 유실, Location 손상, API 재시작 | 4xx·timeout·404를 완료 근거로 쓰지 않고 동일 operation ID로 close/query 복구 |
| 종료 응답 | DELETE 200/204/404, close/query 응답 유실·관리 API 장애·tuple 불일치 | STOPPING 유지, 정확한 `completed` 근거 확인 후에만 closed |
| 장벽 | 등록·인증·AddPublisher·RTP callback에서 A를 멈추고 Stop, 5초 초과 유지 | A는 closing/STOPPING, 늦은 생성으로 LIVE 복귀 없음; B의 영상·음성·FFmpeg·epoch 유지 |
| 프로세스 교체 | MediaMTX A→B→C, 중간 B 미관측, worker/API 재시작 | 검증된 과거 epoch만 completed, 모르는 epoch/포화는 fail-closed |
| 물리 정리 | FFmpeg TERM/KILL 실패, 늦은 Attach, 이전/새 snapshot 불일치 | FFmpeg 프로세스 신원·새 자원 부재·전체 작업 근거를 ENDED 전이에 재검사 |
| 회귀 | 가입/로그인/로그아웃, 소유자·타 사용자·비로그인, CRUD, PC 두 음원·카메라·익명 시청 | 기존 허용·거절과 실제 재생 유지, 종료 뒤 이전 URL 차단 |

실행 시 patch·제품 commit·이미지 digest, 독립 자원 이름, SQL 및 공개 HTTP 관측, 종료 코드, 남은 관문을 구분해 기록한다. 실제 iPhone·외부망·Google 자격·공개 평가 로그인은 09-A 인수에서 별도 판정한다.

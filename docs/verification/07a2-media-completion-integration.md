# 07-A2 제품 종료 완료 통합 검증

상태: 구현 중. 실행 결과는 아직 없다. 제품 코드·테스트가 동결된 뒤 조정자가 공개 HTTP·실제 PostgreSQL·고정 패치 Linux 미디어를 독립 검증한다. 07-A1 격리 결과를 이 통합의 성공으로 대체하지 않는다.

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

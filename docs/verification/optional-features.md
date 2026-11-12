# 선택 요구사항 구현·검증 기록 (댓글·도네이션·녹화 및 다시보기)

판정 기준: `requirements/01-contest-requirements.md`의 COMMENT-01·02, DONATION-01~03, VOD-01~03.
기록일: 2026-11-12. 서비스: https://live.wombatburrow.com (공개 배포, `main`과 동일 코드).

## 요약

| ID | 요구사항 | 구현 위치 | 자동 테스트 | 공개 서비스 확인 |
| --- | --- | --- | --- | --- |
| COMMENT-01 | 로그인 사용자의 방송별 댓글 작성·조회·수정·삭제 | `wbb-backend/internal/comments/service.go`, `internal/httpapi/comment_handlers.go`, 마이그레이션 `0009_comments`, 화면 `wbb-frontend/lib/screens/comments_panel.dart` | `TestCommentsCRUDAndAuthorAuthorization` ✅ | ✅ 작성·익명 조회·작성자 수정(200)·삭제(204) |
| COMMENT-02 | 수정·삭제 시 서버의 작성자 권한 검증 | `comments.authorOf` (행 잠금 후 `author_id` 비교 → `ErrForbidden`) | 같은 테스트: 다른 사용자 PATCH·DELETE `403`, 비로그인 `401` ✅ | ✅ 비로그인 수정 `401` |
| DONATION-01 | 로그인 사용자가 대상·금액을 골라 테스트 결제 요청 | `internal/donations/service.go` `CreateOrder`·`TestCheckout`, `internal/donations/toss.go`(토스페이먼츠 테스트 모드), `internal/donations/memory.go`(키 미설정 시 서버 내장 테스트 결제 게이트웨이), 화면 `lib/screens/donations_panel.dart` | `TestDonationsVerifyApprovalAndCreditOnce` ✅ | ✅ 주문 생성·테스트 결제 승인 |
| DONATION-02 | 서버가 승인 결과·금액·대상을 검증하고 내역 저장 | `Service.Confirm`·`record`: 주문 후원자, 저장 금액, 게이트웨이 상태 `DONE`·주문번호·금액 대조 후 `donations` 확정. 대상은 주문 생성 시 방송 소유자로 고정 | 같은 테스트: 위조 금액 `409`, 타인 확정 `403`, 모르는 결제 키 거절, 결제 실패 미적립, 소유자에게 확정 1건 저장 ✅ | ✅ 위조 금액 `payment_mismatch` |
| DONATION-03 | 중복 승인 통지 시 1회만 적립, 방송별 내역·합계 | `pending→confirmed` 조건부 갱신 + `order_id`·`payment_key` 유일 제약, 웹훅은 게이트웨이 재조회 후 같은 경로 | 같은 테스트: 재확정·웹훅 재전송 `credited=false`, 합계 1건 5,000원 ✅ | ✅ 재확정·웹훅 `credited=false`, 합계 1건 5,000원 |
| VOD-01 | 라이브 방송을 녹화 영상으로 저장 | `internal/mediaworker/worker.go` `outputArgs`: 720p 변환 출력을 RTSP 송출과 fragmented MP4 파일로 동시 기록(tee, 녹화 실패가 라이브를 끊지 않음) | 파일 등록은 아래 테스트, FFmpeg 출력은 공개 서비스에서 확인 | ✅ 실제 송출 25초 → MP4(H.264/AAC) 저장 |
| VOD-02 | 녹화 목록·상세·재생 | `internal/recordings/service.go`, `internal/httpapi/recording_handlers.go`(`/recordings`, `/recordings/{id}`, `/recordings/{id}/media` Range), 화면 `lib/screens/recording_screen.dart`, 홈 "다시보기" 필터 | `TestRecordingsRegisterListAndServe`: 목록·상세·`206 video/mp4` Range ✅ | ✅ 종료 후 약 9초 안에 등록, `206 video/mp4`, 모바일 화면 재생 |
| VOD-03 | 영상 정보와 저장 위치 영구 저장 | 마이그레이션 `0011_recordings`: 회차·방송·`file_path`(저장 위치)·크기·시작 시각·길이 | 같은 테스트: DB 행의 위치·크기·시작 시각 검증, **서버 재시작 후** 목록·재생 ✅ | ✅ |

추가 기능(공모 항목 외): 라이브 채팅 `internal/chat/chat.go`, `internal/httpapi/chat_handlers.go`, `lib/screens/chat_panel.dart` — `TestLiveChatStreamAndSend` ✅.

## 자동 테스트

테스트 경계는 공개 HTTP API + 실제 PostgreSQL + Redis다(`wbb-backend/internal/httpapi/optional_features_test.go`). 내부 함수를 직접 부르지 않고 HTTP로 권한·상태를 검사한다.

```bash
make deps-up
cd wbb-backend && go test -race -count=1 -run 'TestComments|TestDonations|TestRecordings|TestLiveChat' -v ./internal/httpapi/
```

2026-11-12 실행 결과 (일회용 PostgreSQL 17, 실행마다 일회용 Redis):

```
--- PASS: TestCommentsCRUDAndAuthorAuthorization (2.10s)
--- PASS: TestDonationsVerifyApprovalAndCreditOnce (3.10s)
--- PASS: TestRecordingsRegisterListAndServe (5.08s)
--- PASS: TestLiveChatStreamAndSend (1.14s)
--- PASS: TestDonationsWithTossProvider (2.04s)   # 가짜 토스 API로 승인 요청·금액 변조 거절·중복 적립 방지
ok  	github.com/impwy/wbb-live/wbb-backend/internal/httpapi	13.737s
```

전체 백엔드 `go test -race -count=1 ./...`와 `go vet ./...`도 통과했다(httpapi 112.8s 포함 7개 패키지 ok). GitHub Actions CI(`.github/workflows/ci.yml`)가 PR마다 같은 테스트와 Flutter analyze·test·build를 실행한다.

## 공개 서비스 확인

`deploy/live/verify-public.mjs`는 서버와 다른 인터넷 회선에서 실행하며, 합성 카메라·마이크 Chrome으로 평가 로그인 → 방송 등록 → 송출 → 익명 시청 → 종료 → **다시보기 등록·재생(Range)** 까지 확인한다.

```
{"step":"live","state":"live","ice":"connected","pair":"srflx/udp"}
{"step":"ended","playbackAfterEnd":409}
{"step":"replay","recordings":1,"durationSeconds":25,"mediaStatus":206,"contentType":"video/mp4"}
{"step":"cleanup","deleteBroadcast":204,"viewerStalls":0}
```

도네이션은 공개 서버 API로 주문 → 테스트 결제 승인 → 위조 금액 확정(`payment_mismatch`) → 확정(`credited:true`) → 재확정(`credited:false`) → 웹훅 재전송(`credited:false`) → 합계(1건, 5,000원)를 확인했다. 댓글은 작성 → 익명 조회 → 비로그인 수정 `401` → 작성자 수정 `200` → 삭제 `204`를 확인했다.

## 화면에서 확인하는 방법

- 댓글: 방송 상세 하단 **댓글**. 내 댓글의 ⋮ 메뉴로 수정·삭제.
- 도네이션: 방송 상세 **후원하기** → 금액·메시지 → **테스트 결제** 창의 **결제 승인** → 완료 창의 **승인 통지 다시 보내기**로 중복 적립이 없음을 확인. 합계·내역은 같은 화면에 표시.
- 다시보기: 방송을 끝낸 뒤 홈의 **다시보기** 필터 또는 방송 상세의 **다시보기** 목록에서 재생.

## 한계

- 토스 테스트 키가 없을 때 쓰는 내장 게이트웨이는 서버 메모리에 결제 승인 상태를 둔다. 서버 재시작 시 진행 중(미확정) 결제는 사라지지만 확정된 후원은 PostgreSQL에 남는다. 실제 PG 연동은 하지 않았다.
- 녹화 길이는 파일의 마지막 기록 시각과 파일명의 시작 시각으로 계산한다. 1시간을 넘는 방송은 파일이 여러 개로 나뉘어 각각 다시보기 항목이 된다.

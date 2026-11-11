# 07-A1 미디어 종료 계약 독립 검증 계획

상태: 구현 중. 아래 표는 실행 계획이며 통과 결과가 아니다. 지정 구현자가 변경과 자원을 동결한 뒤 조정자가 독립 실행한다. 현재 기초 registry 검사와 일부 패키지 컴파일 확인을 실제 미디어 검증으로 대신하지 않는다.

## 검증 준비

고정 upstream commit, patch 및 이미지 digest를 기록하고 새 Linux 컨테이너·전용 볼륨·loopback 포트를 사용한다. 원래 8090, 제품 미디어 스택과 Kafka의 상태를 전후 확인한다. 테스트 자격은 메모리나 무시되는 로컬 파일로 전달하며 출력에 남기지 않는다. 송출 A와 별도 정상 방송 B의 생성 작업·미디어 경로·변환 프로세스를 구분한다.

| 검사 | 장애 주입과 관측 | 통과 근거 |
| --- | --- | --- |
| 초기화 경계 | 명시 bootstrap, 빈 기존 볼륨, 잘못된 slot/root, 손상 이력, 교체된 lock inode, 두 프로세스 동시 시작 | 정상 시작만 요청 수락. 거절 경로가 기존 이력을 덮어쓰지 않음. 한 프로세스만 잠금 보유 |
| 인증·귀속 | 서명 누락, publish/close/query 용도 교환, 다른 slot/epoch/operation/path/SDP, 같은 ID 재요청 | 잘못된 요청 거절. 중복 POST로 두 번째 세션이 만들어지지 않음 |
| 미도착 생성 종료 | POST 등록 전에 close 후 지연된 POST 전달 | 종료 표식을 유지하고 이후 생성 거절. 단순 목록404를 완료 근거로 사용하지 않음 |
| 생성 중 종료 | 등록·인증·AddPublisher 직전 장벽에서 close, 중복 close, HTTP 취소 | 미완료 실행이 남으면 closing. 장벽 해제 후 정확한 작업만 completed. 늦은 생성으로 부활하지 않음 |
| 응답 유실 | POST 답변 또는 close 답변을 유실시키고 같은 작업 조회·종료 재시도 | known/unknown 결과 모두 정확한 작업 수명에 연결. 응답4xx나 timeout 자체를 완료로 처리하지 않음 |
| 실제 비동기 정리 | RTP callback 실행 중, PLI 대기 중, ICE PATCH 중, RTSP path 종료 중 close | 실제 하위 작업과 경로 정리가 끝난 뒤 완료. 교착이나 계속되는 미디어 송신 없음 |
| 다른 방송 보존 | A의 위 경합 동안 B의 ingress·변환 PID/시작 시각·영상/음성 수신 관측 | B의 송출/변환이 유지됨. MediaMTX 전체 재시작 시험과 분리 |
| 실행 교체 | 부트 기록·fsync 경계에서 실제 종료. A→B→C 중 B를 관측하지 않는 조회 | 이전 무작위 epoch의 정확한 종료 근거 확인. 현재 또는 알 수 없는 epoch를 종료됐다고 답하지 않음 |
| 용량 경계 | 작은 registry 상한까지 채우고 기존 close/query 및 미등록 close 수행 | 기존 작업 정리 가능. 닫힌 표식 소실 없음. 포화 미등록 작업을 근거 없이 완료 처리하지 않음 |
| 지원 제외 설정 | 동적 reload, 외부 hook/자식 실행 등 금지 설정 시도 | 초기 지원 경계가 실제로 차단됨. 문서만의 경고로 대신하지 않음 |

장벽은 이벤트나 관리 응답으로 도달을 확인한 뒤 해제한다. sleep만으로 경합 위치에 도달했다고 추정하지 않는다. 부트 시 요청 수락 전 종료된 epoch와 실제 미디어를 소유했던 epoch를 구분하되, 둘 다 정확한 과거 실행으로만 조회한다.

## 결과 기록 형식

각 검사에 실행 명령, 입력 조건, 장벽 도달 근거, 기대·관측 상태, 실제 미디어/프로세스 결과, 종료 코드와 미검증 항목을 기록한다. 비밀 값과 원본 SDP는 공개 로그에서 제외한다. 마지막에는 patch 해시 불변, 테스트 자원 정리, 기존 서비스 상태를 확인한다.

이 스파이크가 통과해도 WBB 제품 DB·worker의 종료 판단을 연결하는 후속 통합과 실제 기기 인수는 남는다. 사용자 목표의 필수 기능 전체 완료로 표시하지 않는다.

## 장벽 판정 보완

등록 전에는 아직 미디어 자원을 생성할 권한이 없으므로 close가 닫힌 표식을 남기고 completed를 반환할 수 있다. 해제된 지연 POST의 Begin이 거절되는 것이 이 검사의 핵심이다. 등록 이후 인증·AddPublisher·하위 미디어 작업 장벽에서는 추적 중인 실행이 남아 있으므로 closing을 유지해야 한다.

후자의 장벽은 작업 취소만으로 자동 해제되면 장시간 멈춘 실행의 반례를 재현하지 못한다. 실험 전용 hold는 명시 release 또는 전체 실험 중단으로 해제하며, arrived→close→closing 확인→release→completed 순서를 검사한다. 완료 응답 유실 장벽은 이미 증명된 종료 상태와 HTTP 응답 전달을 구분한다.

## 중간 패치 독립 기초 검사

조정자가 upstream `9540cb6c1d9f1e9351fcc4ded3afc249ffff22bc`의 별도 깨끗한 checkout에 SHA-256 `93061e9afc345c34510f2cfdb685e7f92ceacc73ffb05ed158b40cecc96f2f9a` 패치를 적용해 검사했다. macOS 호스트 검사이며 이후 변경이나 Linux 실제 미디어 동작을 보증하지 않는다.

- `go generate ./internal/core ./internal/servers/hls`: 통과.
- `go test -race -count=1 ./internal/wbbcompletion ./internal/servers/webrtc -run 'Test(InternalHTTP|BarrierSignature|Bootstrap|Close|Capacity|Barrier|KilledProcess|CancelledParent|AcceptedNewSession)'`: 두 패키지 통과(1.532초, 1.662초).
- `go build ./internal/core ./internal/api ./cmd/wbb-bootstrap`: 통과.

실제 두 방송, FFmpeg 수명, Linux 부트 장애 및 장벽별 미디어 검증은 아직 미완료다. 최종 변경 동결 뒤 해당 검사를 별도로 수행한다.

별도 인증 fixture도 `go test -race -count=1 auth-fixture.go auth-fixture_test.go`로 독립 실행해 통과했다(1.515초). 이는 HTTP fixture의 프로토콜·행동·회차 경계 검사이며 실제 MediaMTX 인증 연결 성공을 대신하지 않는다. Linux/arm64 실험 이미지의 생성은 Docker 이미지 조회로 확인했으나 실행·미디어 인수 결과는 아직 없다.

작업 자격 동일성 수정이 포함된 패치 SHA-256 `f206e60f8e89975469cd7b6fede1309926dc2293dcdb9e30505eafe73e89b648`도 별도 깨끗한 원본에 적용했다. 위와 같은 generate, 두 패키지 race 선택 검사(1.483초, 1.625초), core/api/bootstrap 빌드가 모두 통과했다. 이 결과는 해당 패치의 기초 재현성 확인이며 이후 설정 watcher 수정이나 실제 HTTP·미디어 회귀를 포함하지 않는다.

## 구현자 Linux HTTP 중간 결과

패치 `8c8c8bda595f4dc6ff756ad32480360008b8efee6673183ef33ab6876994ac35`를 사용한 구현자 `node verify-boundary.mjs` 실행 로그를 조정자가 확인했다. 설정 쓰기 차단 5/5, 혼합 자격 거절 2/2, 4xx·중복·close, 등록 전 및 세션 초기화 전 장벽은 통과했다. 인증 중 장벽 해제 후 늦은 POST가 기대 409 대신 500을 반환해 전체 실행은 종료 코드 1로 실패했다. 구현자가 종료 상태의 초기 응답 오류를 409로 변환하는 수정 중이며 재실행 결과는 아직 없다. 이 검사는 불완전 SDP를 이용한 제어 경계 검사이고 실제 영상·음성 수신 또는 조정자 독립 HTTP 실행이 아니다.

## 조정자 독립 Linux HTTP 회귀

구현자의 자원 조작 중단과 인계를 확인한 뒤 패치 `ac005b02f922b3a3dba7d3bac698c6c67363b0402510062d9bf1d6734af93ca1`, 실행 이미지 `sha256:77d6eaf0442fa9d1242866b6d2aff28c4ee3f1283e6408dcaaa98ad3608e55a4`에서 `node verify-boundary.mjs`를 독립 실행했다(종료 코드 0). 설정 쓰기 차단 5/5, 혼합 자격 거절 2/2, 4xx·중복·close, 등록 전·세션 초기화 전·인증 중 장벽 모두 통과했다. 인증 중에는 close 뒤 closing, release 뒤 completed 및 늦은 POST 409를 검사했다. 검사 후 별도 인증 API 조회로 WebRTC 세션·RTSP 세션·경로 모두 0, 각 목록 페이지 수 0을 확인했다.

불완전 SDP를 이용한 HTTP 제어 검사로 한정한다. 실제 RTP·FFmpeg·HLS와 AddPublisher/응답 유실/프로세스 종료 전체 매트릭스는 아직 미완료다. 검사 전 해시 기록 명령은 작업 디렉터리 상대 경로 오류로 실패했으며 검사 자체는 정상 실행됐다. 인계 시 패치 해시와 검사 후 해시는 동일했고, 별도 파일별 해시는 검사 후 기록했다.

## 2026-11-11 로그 보호 수정 독립 검사

조정자가 패치 `10958cb7183ecd5d7d7b7c2bb584575b753b385fcaea850757856ffa3d801a57`를 별도 깨끗한 원본에 적용해 generate, registry/WebRTC 기초 race 검사와 HTTP 로그 마스킹 race 검사, core/api/bootstrap 빌드를 실행했다. 모두 종료 코드0이며 세 패키지는 각각1.918초·2.096초·2.683초였다. 선택 검사식에 `RequestForLog`를 포함해 대소문자별 내부 서명 마스킹 회귀를 실행했다. macOS 호스트 검사이며 Linux debug/dump 설정 거절이나 실제 기동 실패 경합 실행 증거를 대신하지 않는다. 이후 추가된 미추적 core 회귀 파일 및 WebRTC 내부 초기화 실패 보완은 이 패치 결과에 포함되지 않는다.

## 최종 패치의 조정자 독립 두 방송 스모크

2026-11-11 조정자는 패치 SHA-256 `cc9992059e50da3c19c5126076091008cb1c14706454fee9c66ec3f753b95478`로 전용 Linux/arm64 이미지를 새로 빌드했다(이미지 `sha256:ae05db61560f010ea13738916bb2f9f7dabb8f5f3b772d304c602e73f9446ce3`). 전용 미디어·인증 컨테이너만 해당 이미지로 재생성하고 `node verify-media.mjs`를 독립 실행했다. 종료 코드0, 두 소스 준비, A의 정확한 operation completed·FFmpeg exit0·미디어 부재, B active·동일 FFmpeg 신원·동일 lifecycle/epoch, B 연속9개 관측점 중 영상 증가8구간·새 ingress 바이트8구간·205프레임/6.862초 증가와 음성 두 주파수 확인, 전체 cleanup 검증을 로그 `/tmp/wbb-root-07a1-final-smoke.log`에서 확인했다.

검사 후 별도 관리 API에서 WebRTC 세션0, RTSP 세션0, 경로0을 조회했고 실험 소유 FFmpeg 컨테이너0, 기존 helper8090 HTTP200을 확인했다. 이 결과는 **정상 두 방송 스모크**의 독립 증거다. 응답 유실·장벽·프로세스 장애 전체 매트릭스의 동결 패치 독립 재실행과 설정 watcher 무변경 관측, 제품 DB·worker 통합은 아직 미완료다.

동일 패치·이미지에서 조정자가 `node verify-media.mjs before_add_publisher`를 독립 실행해 종료 코드0을 확인했다. AddPublisher 직전 장벽의 A는 해제 후 completed·미디어 부재, B는 active·FFmpeg 신원과 lifecycle tuple 유지였다. B의 9개 관측점에서 영상 증가 8구간, ingress 증가 8구간, 204프레임/6.843초 증가, 전체 cleanup 통과를 `/tmp/wbb-root-07a1-final-before-add.log`에 기록했다.

동일 패치·이미지에서 `node verify-media.mjs cancel_post`도 독립 실행해 종료 코드0을 확인했다. 완료 응답 전 장벽에서 A의 POST 응답을 유실한 뒤 A completed·미디어 부재, B active·FFmpeg 신원과 lifecycle tuple 유지였다. B의 9개 관측점에서 영상 증가 8구간, ingress 증가 8구간, 205프레임/6.862초 증가, 전체 cleanup 통과를 `/tmp/wbb-root-07a1-final-cancel-post.log`에 기록했다. 두 검사는 현재 패치의 특정 경합 증거이며, 정적 리뷰에서 발견한 설정 우회·검증기 cleanup·이력 무결성 결함이 수정되기 전에는 07-A1 전체 통과로 분류하지 않는다.

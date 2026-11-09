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

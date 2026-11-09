# 07-A1: 미디어 작업 종료 완료 계약 스파이크

**Status:** in-progress
**Blocked by:** 07-C 구현 동결 및 검증 인계. 제품 구현자는 지정 세션 한 명이며 동시에 진행하지 않는다.
**목표:** ADR0001의 종료 증거 계약을 실제 고정 MediaMTX에서 구현·반증한 뒤 제품 통합 가능 여부를 판정한다.

## 범위와 순서

1. `docs/adr/0001-media-operation-completion.md`와 부모07-A를 읽고, 구현 전 endpoint/schema·작업 소유권·잠금 수명·장애 장벽을 보고한다. ADR은 아직 제안이며 실험 성공 전 제품 스택을 교체하지 않는다.
2. `experiments/media-completion/`에 정확한 upstream commit·라이선스·patch와 Linux 이미지 빌드를 보존한다. 별도 Compose 프로젝트, loopback 포트, 새 로컬 볼륨을 사용한다. 기존8090 및 제품 미디어 자원은 보존한다.
3. 프로세스 수명의 registry에 slot/epoch/operation을 등록하고 close와 직렬화한다. 세션 할당부터 HTTP 취소·중간 실패·goroutine 완료·publisher 제거까지 작업 소유권을 유지한다. 관리 lifecycle/close/query 및 내부 인증을 적용한 WHIP 경로를 제공한다. 정확한 인증·귀속·완료 정의는 ADR0001을 따른다.
4. 등록 직전, 인증 중, AddPublisher 직전, 완료 응답 직전에 실험용 명시적 장벽을 설치한다. 임의 sleep으로 해당 경합을 통과했다고 주장하지 않는다. 실험용 장벽의 활성화는 전용 빌드/설정으로 제한하고 공개 서비스에 노출하지 않는다.
5. 연속 epoch 기록과 동일 inode 프로세스 잠금을 구현하고 실제 Linux 프로세스 종료·재시작으로 검증한다. 지원 배치는 단일 Linux 호스트와 동일 로컬 볼륨이다.

## 완료 기준

- 합성 영상·음성 WHIP 두 방송과 독립 수신기를 사용한다. known/unknown Location, POST/close 응답 유실, 중복 close, HTTP 취소,4xx 중간실패를 장벽별로 주입한다.
- 실행이 남아 있는 동안 completed를 반환하지 않는다. close 뒤 늦은 POST가 재등록되지 않고, 장벽 해제 후 정확한 작업만 completed가 된다.404·목록 부재·시간 경과만으로 completed를 만들지 않는다.
- 같은 operation의 다른 경로/본문 재사용 및 다른 epoch/slot/용도 인증은 거절한다. 미도착 작업 close도 향후 생성에 대한 장벽을 남긴다.
- 고정된 작은 registry 용량으로 포화를 재현한다. 기존 작업 close/query는 계속 작동하며 닫힌 표식은 같은 epoch 내 삭제하지 않는다. 공간 없는 미등록 close는 명시적 미완료로 유지한다. ADR의 포화 epoch 영구 봉쇄 후보는 이번 스파이크에서 증명하지 않았으면 지원으로 표시하지 않는다.
- 잠금 획득·epoch 기록·파일 fsync·원자 교체·디렉터리 fsync·요청 수락 경계에서 실제 프로세스를 종료한다. A→B→C에서 B를 관측하지 않아도 A 종료 근거를 검증한다. 잠금 경쟁·손상·기존 볼륨 비움·slot 불일치는 자동 초기화/요청 수락을 막는다.
- 작업 A의 종료/경합 중 방송 B의 epoch, ingress, FFmpeg PID/시작 시각, 연속 영상·음성 수신을 확인한다. MediaMTX 프로세스 재시작 검사는 별도로 수행하며 공유 프로세스 재시작 중 B 연속 재생을 보장했다고 주장하지 않는다.
- 동적 reload 및 외부 hook/자식 실행 등 초기 지원 제외 동작을 실제 거절하거나 기동 시 명확히 차단한다.
- 재실행 명령, 이미지/patch 해시, 장애별 기대·관측 결과를 보고한다. 조정자의 두 축 리뷰와 독립 검증 후 ADR 확정 여부를 판단한다.

## 이후 제품 통합

이 스파이크만으로07-A 또는 필수 종료 요구사항을 완료하지 않는다. 별도 통합에서 DB귀속 commit 후전송, upstream4xx 즉시closed 경로 제거, worker5초부재 기반종료 대체를 수행한다. 공개API·실제DB에서 STOPPING/권한폐기→완료근거→FFmpeg종료·최신자원조회→ENDED를 검증한다. 실제기기/외부망/VAAPI/ABR 인수는 별도로 남는다.

## 구현 설계 검토 결정

lifecycle 조회, operation close/query, 기존 전용WHIP의tuple+정확경로+SDP해시 HMAC 계약으로 격리실험구현을진행한다. slot anchor는bootstrap시고정하는불변설정이며매부트갱신하는두번째epoch저장소로확장하지않는다. 기동시anchor/slot/기존이력을먼저검증하고같은inode잠금을획득한뒤새epoch를영속화한다. 불일치이력에새epoch를먼저써복구를어렵게하지않는다.

query의실행중상태와never-registered를completed와구별한다. duplicatePOST는새세션을만들지않으며다른경로/본문충돌도거절한다. close가참조하는작업실행수에그close조회handler자신을넣어영원히완료되지않는구조를피한다. 생성handler와그가위임한세션/하위작업만종료대상소유권으로추적한다.

기존media서비스의권한/상태계약은이실험중유지한다. 실제upstream소유권연결과최소회귀를먼저보고하고,전체장애장벽/두방송/epoch회귀완료후독립검증에인계한다.

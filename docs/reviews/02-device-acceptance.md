# 02 실기기 검증 환경 리뷰

2026-11-09, 기준 `bc9d46c`, 작업 브랜치 `work/media-device-acceptance`.
미커밋 변경과 신설 파일을 고정 복사해 명세·표준 두 축으로 검토했다. 아래 중간 발견은 수정 이력이며 최신 결과는 마지막 절을 따른다. 구현은 지정 pi에서 실제 한도 오류 후 지정 대체 Codex로 전환했다. 실제 기기 통과 증거와 코드 리뷰 결과는 구분한다.

## Standards

| 위치 | 영향·근거 | 수정 및 재검증 |
| --- | --- | --- |
| start/preflight/stop의 Compose 호출 | 상속된 COMPOSE_PROJECT_NAME/COMPOSE_FILE로 다른 프로젝트에 down을 보낼 수 있음. 격리 stub 재현 | project/config/env를 고정하고 외부 override 영향 차단. 다른 프로젝트 보존 확인 |
| env.example, preflight, Compose 환경 | bcrypt의 달러 문자가 Compose 보간으로 절단됨. 작은따옴표 사용 시 grep 파서와 해석 불일치. 경고에 해시 일부도 출력 | 일관된 비실행 파싱과 비밀 출력 차단. 같은 합성 해시가 검증/기동에 전달되는지 확인 |
| start.sh 신호 trap | TERM 후 cleanup을 실행해도 readiness와 성공 종료를 계속함. 순수 stub 재현 | 신호 종료와 EXIT 정리를 분리하고143/130 및 후속 실행 부재 확인 |
| 신규 셸 파일 모드 | 0644 상태여서 직접 호출하는 preflight가 Permission denied | 실행 비트 또는 명시적 셸 호출, 문서 명령 재실행 |
| start.sh의 sed -i 빈 인자 | BSD 전용 동작으로 Linux 대상 실행 실패 | 이식 가능한 변환 방식으로 양쪽 실행 확인 |

## Spec

| 계약 | 발견 사항 | 수정 및 재검증 |
| --- | --- | --- |
| 자격 검증/기동 일치 | dotenv/bcrypt 파싱 불일치. 위 Standards와 독립적으로 같은 실패 확인 | 합성 해시를 이용한 compose config와 실제 Caddy 경계 검사 |
| 허용 endpoint/resource 경로 | 정규화된 /admin 경로 및 whipBase 없는 외부 검증영역 Location 수락 | 경로 탈출·prefix 누락·다른 stream·외부 origin을 실제 사용 경계에서 거절 |
| 신호 후 실행 금지 | TERM 후 기동 완료와 exit0. 위 Standards와 독립 stub 재현 | 동일 신호 입력으로 후속 명령 부재 및 자원 정리 확인 |

## 조정자 독립 실행

- 기존 상태·로그 함수 누락을 발견해 반환했다. 초기 app.js 스냅샷의 함수 경계 진단에서 HTTPS 설정 누락 폴백과 다른 stream Location 수락도 재현했다. 함수 진단을 브라우저 전체 통과로 취급하지 않는다.
- 포트만 있는 Caddy site는 설정 validate 후에도 실제 HTTPS handshake가 실패했다. 명시 hostname 변경 후 인증서 준비가 끝나면 UI200에 도달했다. CA를 명시 신뢰했고 인증서 검증을 끄지 않았다. 인증서 준비 완료 전에 readiness를 성공 처리하지 않아야 한다.
- Caddyfile 스냅샷 SHA256 앞16자리 `b0aed5eb3ca73e12`: 인증된 잘못된 Origin POST와 Origin 없는 PATCH가403이 아닌 upstream502에 도달했다. upstream은 전달 여부 검사용으로 의도적으로 기동하지 않았다. 실제 Caddy directive 순서에서 인증→Origin 거절→proxy가 보장되도록 수정 요청했다.
- 조정자 검사 컨테이너는 모두 정리했다. 기존 CPU 수동 검증 서버와 운영 Caddy는 변경하지 않았다.

두 축 결과와 실제 요청 실패를 pi에 전달했다. 수정 후 같은 입력으로 재검사하고 전체 구현 범위를 다시 리뷰하기 전에는 미해결 상태다.

## 대체 Codex 수정 단계 재검증

pi의429/1310 및 재시도 종료 후 지정 대체 Codex로 이관했다. 새로운 고정 복사본에 대한 독립 재검증 결과다.

- Standards 기존5건: Compose 대상 고정/상속 변수 제거, 합성 bcrypt 보존·무따옴표 조기 거절, compose up 도중 TERM→143·정리1회·후속 curl 없음, 실행 비트, 이식 가능한 HTML 변환을 확인했다. Linux 실환경 전체 기동 검사는 아직 아니다.
- Spec 기존3건: 경로 탈출 및 제약 누락 거절·정상 resource 수락, 동일한 합성 해시 파싱, TERM143을 각각 확인했다. CPU/VAAPI는 명시한 profile로 선택하고 VAAPI 실패를 CPU로 대체하지 않는다.
- 추가 Standards P2: start의 신호 trap 이전에 실행되는 preflight 자식이 부모 TERM 후 compose ps와 Caddy validate를 계속 실행하는 것을 stub으로 재현했다. 최초 단계부터 자식·검증 컨테이너 정리와 외부 명령 제한 시간이 필요하다.
- 추가 Spec P2: ICE DNS label64자를 settings 및 전체 preflight가 허용했다. IP literal과 DNS label 형식·길이 검증이 필요하다.
- 조정자 추가 계약 점검: HTTPS443의 브라우저 Origin은 기본 포트를 생략하므로 설정 검사와 공개 적용안도 이를 반영해야 한다.

추가 항목은 대체 Codex에 반환했다. 실제 Caddy 부분8/8 결과는 [검증 기록](../verification/02-device-acceptance.md)을 따른다. 실제 미디어·최종 전체 리뷰·실기기 관문은 미완료다.

### 수명 관리·설정 수정 후 독립 재검증

- 최초 preflight 중 부모 TERM:143 종료, 자식의 지연 marker 및 이후 ps/validator/up 호출 없음. `lifecycle.py`의 프로세스 그룹·명령별 제한 시간을 확인했다.
- 직접 자식이 먼저 종료해 손자만 남는 추가 timeout 결함을 fork fixture로 재현해 반환했다. 수정 후 같은 입력에서0.15초 timeout이 반환되고0.6초 후 예정된 손자 marker가 생성되지 않는 것을 독립 확인했다.
- 호스트·Origin 독립26건 통과: DNS label63자/64자, 잘못된 숫자 IP, 하이픈 위치, 정상 IPv4·DNS,443 포트 생략 및18443 포트 명시를 확인했다. 이 수정 단계의 Spec 신규 결함은0건이다.
- 제공 selftest의 기본 ICE 포트가 사용 중인 상태에서는 실패했으며, 독립 임시 복사본에서 테스트 포트만 빈 포트로 바꾼18항목은 통과했다. 원본 전체 검사 및 실제 미디어 검사와 구분한다.

새 브라우저 flow의 중계 검사는 인증·Origin·쿠키를 대리 처리하므로 브라우저가 실제 생성된 HTTPS 화면에 직접 접속하는 인수를 대체하지 못한다. 별도 직접 Chrome 검사와 실제 재생·음성 관측을 요청했다. 아직 최종 전체 승인 상태가 아니다.

### 최종 후보 재리뷰 — 추가 수정 요청

- Spec P2: HTTPS 상대 HLS_URL을 manifestProbe의 new URL base에 그대로 전달하여 정상 playlist에서도 Invalid URL. 절대 base 적용 및 직접 Chrome manifest 경로 검증 요청.
- Standards P2: terminate_child에서 직접 부모가 종료하고 손자가 stdout/stderr를 닫은 채 SIGTERM을 무시하면 communicate가 즉시 끝나 그룹 SIGKILL이 생략됨. 동기화한 fork stub의 지연 marker로 재현. 프로세스 그룹 잔존 확인 및 bounded SIGKILL 요청.
- 위 두 건을 지정 대체 Codex에 전달했다. 최종 승인 전 재검증한다.

## 최종 코드 재검토 결과

- Standards: 잔여 확정 결함0건. 마지막 SIGTERM 무시·파이프 닫기·직접 부모 선종료 입력에서 0.25초 유예 후 marker 미생성과 killpg 검사상 그룹 소멸을 독립 확인했다.
- Spec: 잔여 확정 결함0건. HTTPS 상대 경로와 기존 localhost 경로 모두 master→variant 요청 및 query 보존, manifestLL=true, blocking reload 표식 관측을 독립 확인했다.
- 조정자의 실제 Caddy/MediaMTX/FFmpeg 및 직접 Chrome 검사는 [검증 기록](../verification/02-device-acceptance.md)을 따른다. 실제 기기·외부망·기존 CPU83 전체 회귀는 별도 미완료 관문이다. 공개 적용안은 전체 운영 설정 사본에서 검증했으며 운영 적용은 하지 않았다.

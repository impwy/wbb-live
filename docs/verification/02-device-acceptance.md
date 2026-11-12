# 02 실기기 검증 환경 진행 기록

2026-11-09, 작업 브랜치 `work/media-device-acceptance`, 시작 기준 `bc9d46c`.
구현은 지정 pi에서 실제 한도 오류 후 지정 대체 Codex로 전환했다. 설계·독립 검증은 조정 Codex가 맡는다.
현재 로컬 검증 기기는 macOS26.6.2, Google Chrome156.0.8078.80이다. 휴대폰은 사용자 확인상 아이폰 Google Chrome이며 OS·브라우저 버전은 인수 시 확인한다.

## 현재 관측

| 항목 | 결과 | 의미 |
| --- | --- | --- |
| 기존 로컬 검증 화면 | loopback8090에서 HTTP200, Chrome에 화면 열림 | 화면 접근만 확인; 송출 성공이 아님 |
| 기존 MediaMTX | source/output 모두 ready=false, 수신 바이트0 | 관측 시점에 사용자 송출 없음 |
| 미디어 연결 설정 | 브라우저 WHIP/HLS와 ICE가 loopback으로 고정 | 그대로는 휴대폰 접근·송출 불가 |
| live 도메인 | DNS 응답 있음, HTTPS HEAD525 | 공개 HTTPS 검증 환경 아직 미준비 |
| 기존 서버 Caddy | 적용 설정에 live 호스트 없음 | 새 검증 경로·인증서 설정이 필요하며 아직 변경하지 않음 |
| 구현 담당 | 지정 pi가 상태 확인에 정상 응답, 티켓 읽기 후 구현 시작 | 한도 해제를 시간만으로 추정하지 않음 |

현재 실험은 Flutter 제품 송출 화면과 연결되지 않는다. 검증용 Basic Auth는 제품의 사용자·소유자 권한 검증을 대신하지 않는다.

## 검증 범위

구현 및 자동 검사 기준은 [실기기 환경 티켓](../../.scratch/wbb-live/issues/02-device-acceptance-environment.md)을 따른다. 실제 Caddy 인증 경계, WHIP 전체 수명주기와 잘못된 Location 거절, HLS 하위 리소스·query 보존, 기존 CPU 회귀, 자원 정리를 각각 독립 확인한 뒤 결과를 추가한다.

## 구현 중 독립 검사 — 미완료

최종 스냅샷의 통과 기록이 아니라 수정 중 발견한 실패다.

- 브라우저 endpoint 해석 로직의 중간 스냅샷에서 HTTPS 설정 누락 시 localhost 폴백, 설정 경로 탈출, 다른 stream의 Location 허용을 재현했다. 기존 상태·로그 정의가 빠진 회귀도 확인해 pi에 반환했다. 함수 경계 진단이며 실제 브라우저 전체 검증을 대신하지 않는다.
- 별도 loopback Caddy 컨테이너에서 현재 설정의 `validate`는 성공했으나 실제 HTTPS 요청은 TLS handshake internal error로 실패했다. 로컬 CA를 명시 신뢰한 검사이며 인증서 검증을 끄지 않았다. 포트만 지정한 site와 internal TLS의 호스트 설정을 수정하도록 반환했다. HTTP 인증·Origin 검사는 handshake 실패로 아직 도달하지 못했다.
- 위 독립 검사 컨테이너는 종료했다. 기존 CPU 수동 검증 서비스와 기존 서버 Caddy는 변경하지 않았다.

### 대체 Codex 수정 후 부분 재검증

- endpoint/resource 해석의 독립 진단7/7 통과: 정상 resource, HTTPS 설정 누락, 설정의 일반/인코딩 경로 탈출, 외부 Location, 다른 stream, resource 제약 누락. 실제 브라우저 송출 검사가 아닌 함수 경계 진단이다.
- Caddyfile SHA256 앞16자리 `24e2724f7016630a`의 실제 loopback HTTPS 검사8/8 통과. 명시 CA 신뢰 및 인증서 준비 완료 후 UI/HLS/WHIP 미인증401, 인증 UI200, 잘못된 Origin403, Origin 누락403, 비공개 API404를 확인했다. 정상 Origin WHIP의502는 의도적으로 없는 upstream까지 도달했다는 검사 결과이며 실제 송출 성공이 아니다.
- 별도 검사 컨테이너는 종료했다. 실제 MediaMTX와 FFmpeg를 연결한 새 구성의 인수 및 실기기 결과는 아직 남아 있다.

## 실제 기기 인수 결과

| 시나리오 | 상태 |
| --- | --- |
| PC 화면+공유 소리+마이크, 같은 PC 예비 재생 | 사용자 실행·청취 결과 대기 |
| PC 송출 → 실제 휴대폰 시청, 두 음원 구분 | 미실행 |
| 실제 휴대폰 카메라·마이크 → PC 시청 | 미실행 |
| 버튼 종료·공유 중지·권한 거부 및 장치 해제 | 미실행 |
| native Safari 인증·재생 | 미실행 |
| 외부 이동통신망 ICE·NAT/TURN | 미실행 |

기기·OS·브라우저·네트워크·실행 시각·영상·음성·종료 증거가 확인되기 전 체크하지 않는다. 합성 검사를 실제 캡처·청취 결과로 기록하지 않는다. 부모02 및 후속03/04의 선행 관문은 유지한다.

### 조정자 추가 독립 검사

- selftest.py: 설정·Compose 격리·신호·timeout의 기존19검사 통과. 단, 최종 표준 리뷰가 발견한 파이프를 닫은 SIGTERM 무시 손자 경계는 이 버전에서 미해결이므로 전체 수명 관리 완료로 보지 않는다.
- verify-device-config.mjs: 실제 headless 브라우저 설정·경로 검증9/9 통과.
- 사용자 아이폰 Chrome의 Wi-Fi 및 이동통신망 검증을 진행하도록 범위가 추가됐다. 실제 수동 성공 결과는 아직 없다.

## 최종 후보 독립 실행 — 2026-11-09

운영과 별도인 로컬 wbb-media-acceptance 프로젝트를 임시 복사본과 합성 자격으로 실행했다. 브라우저는 시스템 Chrome의 독립 프로필이며 합성 canvas/oscillator를 사용했다. CA로 TLS leaf를 검증한 뒤 해당 프로세스에만 leaf SPKI를 한정했다. 사용자 브라우저와 시스템 신뢰 저장소는 변경하지 않았다.

| 검사 | 결과 |
| --- | --- |
| 설정·Compose 격리·신호·timeout·손자 종료 selftest |20/20 통과 |
| 실제 브라우저 설정·상대 manifest 경로 회귀 |10/10 통과 |
| 실제 Caddy HTTP 인증/Origin/비공개 API 경계 |16/16 통과 |
| 생성된 HTTPS UI에 직접 접속한 Chrome |14/14 통과 |
| 재생 증거 |currentTime13.53→15.99, decoded frames4→79, 오디오 분석값2109 |
| WHIP/미디어 |POST/PATCH/ICE, LL-HLS manifest, HLS cookie/redirect/실제 mp4 요청 통과 |
| 종료 |브라우저 tracks/PeerConnection/AudioContext 정리 및 송출 중 관측한 FFmpeg PID 종료 확인 |
| 자원 |전용 프로젝트 종료·볼륨 정리, 기존 실행 컨테이너 식별자 전부 유지 |

이 실행은 relay가 Authorization/Origin/cookie를 대신 주입하는 검사와 별개다. 사용자 실제 화면·마이크·아이폰 결과를 대신하지 않는다. 기존 수동 검증 스택을 보존하기 위해 원래 CPU83 전체 검사를 재실행하지 않았다. 새 구성의 VAAPI 실행도 이번 결과에 포함하지 않는다. HTTP 인증 우회 입력 및 LL-HLS blocking query 전체 행렬을 모두 수행했다고 주장하지 않는다.

공개 환경은 운영 이미지와 전체 Caddy 설정 사본을 동일 /etc/caddy 위치로 마운트하고 포트 공개 없이 adapt/validate를 통과했다. 임시 Basic Auth와 독립 live host를 포함했으며 사본·자격은 비공개로 보존한다. 운영 파일/reload/DNS/방화벽은 아직 변경하지 않았다. [외부망 적용 계획](02-device-external-plan.md)을 따른다.

## 공개 HTTPS 적용 관측

- 사용자가 live A 레코드를 DNS only로 변경했다. 실제 DNS와 공인 신뢰 인증서를 확인했다. 실제 origin IP와 임시 자격은 공개 문서에 기록하지 않는다.
- 서버에는 별도 wbb-media-acceptance CPU720p 미디어 프로젝트와 live 전용 Caddy 파일만 적용했다. 운영 Caddy는 기존 컨테이너를 reload했다.
- 공개 HTTP 경계8/8 통과: UI/HLS/WHIP 미인증401, 인증 UI200, 잘못된/누락 WHIP Origin403, 허용 Origin의 실제 MediaMTX 도달400, 관리 API404.
- 서버 내부 ICE/TCP18391은 정상 연결되나 다른 인터넷 회선의 Mac에서는 미도달. 공유기 포트 전달/방화벽을 확인 중이다. 신호 HTTP 성공을 외부 송출 성공으로 기록하지 않는다.
- 아이폰 Chrome 실제 캡처·청취 결과는 아직 없다.

공개 적용 후 기존 Caddy 파일의 모든 해시 및 기존 실행 컨테이너 식별자 보존을 확인했다. PR4의 backend/frontend CI도 통과했다. 서버 공유기 관리 포트로 향하는 loopback 전용 SSH 터널을 준비했고 사용자 로그인 진행을 기다린다. 관리 화면의 인터넷 공개나 기존 관리자 자격 변경은 하지 않았다.

## 사용자 로컬 테스트 전환

사용자가 공유기 관리자 비밀번호를 기억하지 못해 로컬 테스트만 진행하기로 했다. 이번 외부 acceptance 프로젝트를 종료하고 live 전용 Caddy host를 비활성화한 뒤 전체 validate/reload했다. 기존 Caddy 파일은 보존했으며 공유기 관리 SSH 터널도 종료했다. 사용자가 변경한 live DNS only는 그대로 유지한다. 기존 Mac helper8090의 HTTP200과 CPU 미디어 컨테이너를 확인했다. 실제 휴대폰/외부망 인수는 보류하며 완료로 처리하지 않는다. 기본/대체 구현자는 대기 상태를 유지한다.

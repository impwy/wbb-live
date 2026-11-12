# 06 Google 로그인 검증

상태: 구현 자동 검증 통과, 실제 Google 자격 로그인은 미검증이다.

## 초기 구현 독립 검증

2026-11-08 스냅샷28ce073. 대체 Codex는 실제PG/Redis 공개HTTP race·vet, Flutter 테스트·분석·빌드 성공을 보고했다. 별도 로그 파일은 없었으므로 이 보고만으로 최종 완료 처리하지 않았다.

조정 Codex는 전용 PostgreSQL 컨테이너와 실행마다 격리된 Redis, 고정 코드 복사본을 사용해 추가 공개HTTP 검사를 실행했다. Google 정상 부가query1건과 보안 입력6건(빈 error 동시존재·malformed 중복code·잘못된 azp4종)이 기대와 다르게 처리돼 exit1이었다. [리뷰](../reviews/06-google-login.md)의 S1/S2/F1을 실행으로 확인했다.

독립 Flutter 검증은 기존21개 통과, 일반 홈 로그인 control 통과, callback 직접진입 오류2종→재시도→이메일 로그인 성공 후 빈 화면으로 실패했다. F2를 실행으로 확인했다.

## 수정 후 검증

지정 pi가 18:40 KST 정상 응답 후 구현을 인계받아 수정 중이다. 수정 후 같은 재현 검사, 전체 공개HTTP/Flutter 검사, 두 축 재리뷰를 진행한다. 실제 Google Console 자격을 사용하는 브라우저 로그인은 별도 관문으로 남긴다.


## 첫 pi 수정 독립 검증 (5a5776a)

동일한 공개HTTP 재현 입력을 유지하고 제품 callback 검증 코드만 갱신한 결과, 정상 Google 부가query와 부정 입력7건을 포함한 검사가 모두 GREEN·exit0이었다. 별도 최신 backend 복사본의 전체 go vet, go test -race -count=1 ./..., govulncheck도exit0이었다. 취약점 스캔은 호출 심볼·import package0건이며 요구 모듈 수준의 미호출1건을 별도로 보고했다.

Flutter 이메일 재시도 경로는 정상 내 방송→뒤로→홈으로 연결됐다. 최초 독립 검사의 즉시 홈 표시 가정을 목적지 순서에 맞게 보완했고, 같은 보완 검사는 구스냅샷에서 여전히 실패해 결함을 가리지 않음을 확인했다. 최신 공식22+독립이메일3=25개 통과, analyze와 release web build도 통과했다.

추가 독립 가입 검사는 일반 홈 control은 통과했으나 callback오류2종→재시도→가입→register201/login200 이후 빈 화면으로 실패했다. 같은 스냅샷의 새 P2로 기록했고 pi가 보완 중이다. 실제 Google 자격 로그인은 계속 미검증이다.


## 최종 자동 검증 (f1ba363)

pi가18:40 정상복구 후 수정하다가22:40:38 reset을 표시하는429/1308을 다시 반환했다. 기존 대체 Codex가 인수해 공식 Back 회귀 검사를 마무리했다. 두 구현자는 동시에 편집하지 않았다.

| 독립 검사 | 결과 |
| --- | --- |
| 고정 backend 전체 | go vet, go test -race -count=1 ./..., govulncheck exit0. 실제 전용PostgreSQL·격리Redis 사용 |
| callback·azp 추가 재현 | 초기7실패 입력을 그대로 유지한 검사 모두GREEN. 정상부가query허용·malformedquery/azp거절 |
| 최종 Flutter | 공식27+독립9=36개 모두통과 |
| UI 완료/뒤로 | cancelled/account_exists의 로그인·가입, 일반홈control, 홈canPop=false·숨은Login/Signup없음·platformBack후홈유지 |
| Flutter analyze | 최종 test lint중괄호 정리 후 문제없음·exit0 |
| release web build | 최종과 동일한 제품코드에서 --no-web-resources-cdn 빌드exit0. 기존CupertinoIcons 폰트 경고는 있었음 |
| 두 축 리뷰 | Standards0 / Spec0 잔여 |

검증용 전용PostgreSQL 컨테이너는 제거했고 harness Redis는 실행별 정리됐다. 기존 WBB 개발PG/Redis는 정지·볼륨보존, 기존 로컬 CPU 미디어 서비스는 유지한다. 실제 Google client 자격은 사용하지 않았다. README의 동일 출처 실행 조건에서 실제 Google 로그인·취소·충돌·로그아웃을 별도로 확인해야 한다.

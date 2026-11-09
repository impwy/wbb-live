# 07-C Kafka outbox 전달 검증

현재 상태: 최종 후보의 전체 Go race·vet와 실제 DB/프로세스 및 Kafka 중단 미디어 독립 검증 통과. 게시 전 최종 정리 중. 전체07 장애 복구나 필수 기능 전체 완료를 뜻하지 않는다.

## 구현자 검사와 확인한 로그

- 전체 Go race 회귀, vet, API/media-worker/outbox 바이너리 빌드 및 outbox 컨테이너 이미지 빌드 통과 보고를 받았다.
- 실제 Kafka·PostgreSQL 검사에서 레코드별 승인과 DB 마킹, 승인 후 DB 실패 주입, 자식 프로세스 강제 종료 후 동일 ID 중복, 다중 claim, 만료 및 늦은 응답 거절, 격리101건의 정확 ID 조회/재처리를 검증했다.
- Kafka 중단 HTTP prepare/stop은 실제 미디어를 생성하지 않는 별도 회차 검사다. 재개 후 consumer3건 수신을 확인한다.
- Kafka 중단 Flutter camera 로그는 H264·Opus, 익명7프레임,660Hz 마이크 음원, 종료 후 playback409/HLS403/이전grant409, ended/released를 기록했다. 실제물리카메라나두음원혼합근거로사용하지않는다.
- 일회용 PostgreSQL의 초기 중단→발행기 시작→복구, 미발행 항목이 있는 SIGTERM→종료0→재시작 전달, 실행 중 DB 재시작 검사가 최종6.98초에 통과했다. INSERT후UPDATE fixture 경합은 단일INSERT로 수정했다. 초기검사는 --rm 컨테이너를stop하여 재시작할 수 없어 실패했으며 재시작 가능한일회용컨테이너로 수정했다. 기존미디어DB는재시작하지않았다.

## 조정자 독립 확인

- outboxdelivery/CLI 단위 race 검사 통과. 이 최초 실행은 실제 PG환경변수 없이 진행하여 통합검사를포함하지않는다.
- 두 축 정적 리뷰에서 초기 P2와 추가fixture경합 수정 후 잔여확정결함0건. 운영README에 장애재현명령·공식설정근거·단일broker한계를추가했다.
- 최종 후보15파일해시를고정하고 실제PG·Kafka환경을 연결한전체 Go race 검사를시작했다. 검사는무작위격리DB·별도Redis를사용한다. 실행완료및최종해시확인전전체통과로판정하지않는다.

## 남은 독립 검증

최종 게시 내용과 CI를 확인한다. 실제 Kafka 복구 consumer 검사는 구현자 outage 실행 및 조정자 정상 consumer 회귀를 구분해 기록한다. known/unknown미디어생성종료증거는07-A, inbox소비/재조정은별도범위다. 실제iPhone/외부망/Google/VAAPI/ABR 인수를완료로대체하지않는다.

## 최종 후보 독립 실행 결과

- 실제 PostgreSQL·Kafka 연결 전체 `go test -race -count=1 ./...` 종료0. HTTP100.053초, outboxdelivery5.955초. `go vet ./...`도종료0. 프로세스재시작opt-in은아래별도실행했다.
- 새일회용PG에서실제초기DB중단→복구,미발행보존TERM→재개,실행중DB재시작검사7.29초통과. 테스트DB잔여0을확인하고조정자가생성한컨테이너/볼륨을제거했다.
- 실제Kafka컨테이너를중단한상태에서별도Chrome송출/시청제품UI카메라검사통과. H2641프레임/17패킷,Opus229패킷,익명7프레임,660Hz -32.85dB(잡음-93.03dB),pause유지,종료후409/403,이전grant409,ended및tracks/PC/audio해제를확인했다. 검증기에서FFmpeg/ingress부재도확인한다. 종료뒤Kafka를다시기동했고healthy다.
- 검증후제품15파일해시변경없음. 전용회차ended35/활성0,원래8090 HTTP200. 실제기기검사가아니며두음원혼합또는모든미디어종료경합을증명하지않는다.

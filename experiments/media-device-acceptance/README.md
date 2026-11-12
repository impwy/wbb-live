# 실기기 인수 환경 제안

`start.sh`, `preflight.sh`, `stop.sh`는 고정된 `wbb-media-acceptance` Compose 프로젝트만 다룬다. `env.example`은 loopback 전용 예시다. 무시되는 `.env`에 복사한 뒤 bcrypt 해시는 저장소 밖에서 대화형 `caddy hash-password`로 만들고 인코더를 명시한다. 실제 비밀번호·해시·스트림 키·서버 주소를 저장소에 넣지 않는다. 로컬 Caddy의 CA는 검사 클라이언트에서 명시적으로 신뢰해야 한다. 스크립트는 TLS 인증서 검사를 끄지 않는다.

이 로컬 구성만으로 공개 접근, 실제 두 기기의 영상·음성, 외부 ICE/NAT, 모바일 native HLS 인증 유지를 증명할 수 없다. 검증은 합성 브라우저와 별도로 실제 기기에서 해야 한다.

## 공개 Caddy 추가안 — 미적용

현재 서버에는 `live.wombatburrow.com` host matcher가 없다. DNS는 응답하지만 HTTPS는 525였으며 원인 해결이나 공개 인증서 발급을 확인하지 않았다. 운영 Caddy는 host network이고 `/etc/caddy`가 bind mount, `/data`와 `/config`가 volume이다. 기존 사이트를 유지하면서 **기존 운영 Caddy 안에** 다음 host block을 새로 추가하는 안을 검토한다. 이미 승인된 live block이 나중에 생겼다면 그 block 안에 fragment만 추가한다. 중복 host block을 만들지 않는다.

```caddyfile
live.wombatburrow.com {
    route {
        import /etc/caddy/wbb-media-check/Caddyfile.public.fragment
        # 승인된 다른 live 경로가 있으면 여기에서 별도 검토
        respond 404
    }
}
```

`Caddyfile.public.fragment`는 `/__media-check/*` 경로만 처리한다. 기존 광범위한 handler보다 앞에 import해야 인증→Origin 거절→proxy 순서가 유지된다. 현재 서버의 기존 사이트와 root 도메인 DNS 레코드·Caddy host·인증서는 보존한다. [설계 기준](../../docs/design.md)의 CDN 미사용, `live`의 DNS only, CNAME이면 전체 대상 체인의 프록시 해제 조건을 적용 전 확인해야 한다. 기존 Caddy에는 source `remote_ip` matcher 9개가 있으나 UFW 정책은 확인되지 않았다. 기존 ACL을 완화하지 않는 추가안만 검토한다. 현재 방화벽 상태나 공개 접근을 추정하지 않는다.

Compose 내부 DNS 이름 `mediamtx`는 host-network Caddy에서 바로 해석되지 않는다. `docker-compose.public.proposal.yml`은 검토용 override로, 실험 Caddy를 제외하고 WHIP/HLS만 호스트 loopback 19889/19888에 바인딩한다. 적용 전 충돌을 확인해야 하며 다른 검토된 private 연결 방식도 가능하다. MediaMTX 관리 API·RTSP·직접 HTTP 포트는 공개하지 않는다. ICE는 별도 명시 인터페이스·포트와 실제 기기 네트워크 검증이 필요하다. 로컬 `start.sh`는 이 공개 override를 적용하지 않으며 공개 기동 절차는 별도 승인 대상이다.

생성된 `web-build`는 운영 Caddy의 **기존** `/etc/caddy` bind mount 안에 독립 디렉터리로 배치하는 안을 검토한다. 예를 들면 컨테이너 경로 `/etc/caddy/wbb-media-check/web`, 호스트 쪽은 `<existing-Caddy-bind-source>/wbb-media-check/web`이다. 기존 사이트 파일을 덮어쓰지 않고 Caddy가 읽을 수 있는지 확인한다. fragment도 같은 독립 디렉터리에 둔다. 실제 호스트 원본 경로는 공개 문서에 쓰지 않는다.

운영 Caddy의 보호된 환경에서 다음 값을 준비한다.

- `WBB_MEDIA_AUTH_USER`, `WBB_MEDIA_AUTH_HASH`: 저장소 밖에서 만든 임시 검증 자격과 bcrypt 해시
- `WBB_MEDIA_ORIGIN=https://live.wombatburrow.com`: 표준 HTTPS 443은 `:443`을 생략
- `WBB_MEDIA_WHIP_UPSTREAM=127.0.0.1:19889`, `WBB_MEDIA_HLS_UPSTREAM=127.0.0.1:19888`: 위 loopback 제안 사용 시의 값
- `WBB_MEDIA_WEB_ROOT=/etc/caddy/wbb-media-check/web`: 운영 Caddy 컨테이너 안의 독립 웹 루트

18443 같은 비표준 HTTPS 포트에서는 Origin에 포트를 포함한다. 로컬 `.env` 검사도 이 규칙을 강제한다. 공개 경로에는 `live.wombatburrow.com`에 유효한 공개 신뢰 인증서와 정상적인 DNS·ACME 검증 경로가 필요하다. 로컬 `tls internal` 인증서는 일반 기기의 공개 접속에 쓰지 않는다. 인증서 준비와 기존 ACL 내 접근 가능 여부는 아직 미검증이다.

사용자 승인 전에는 운영 설정을 바꾸지 않는다. 검토 단계에서는 기존 운영 Caddy 설정의 복사본을 확보하고, 19888/19889 및 ICE 포트 충돌을 검사하며, 합성 자격으로 완성된 Caddy 설정을 adapt/validate한다. 비밀 주입·private upstream·정적 웹 루트의 도달 가능성을 별도로 확인한다. 승인 후 실제 적용 시에는 기존 운영 Caddy만 reload하고 유효 인증서 및 HTTPS 401(UI/HLS/WHIP), 403(잘못된/없는 WHIP Origin), 허용 Origin의 upstream 도달을 확인한다. 인증서 검사 우회나 임시 무인증 공개는 하지 않는다.

롤백안은 저장한 Caddy 설정을 복원·검증·reload한 뒤 검증 경로가 사라지고 기존 사이트가 계속 응답하는지 확인하는 것이다. 별도 시작한 `wbb-media-acceptance`만 종료하고 해당 19888/19889 매핑만 제거한다. 점검 완료 전에는 독립 웹 디렉터리를 보존한다. 이 제안은 운영 Caddy·DNS·방화벽·외부 서비스를 변경하지 않았다.

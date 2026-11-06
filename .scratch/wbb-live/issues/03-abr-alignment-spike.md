# 03: 다중 품질 LL-HLS 정렬과 전환 검증

**What to build:** 같은 실제 방송을1080/720/480 중 제공 가능한 품질로 보고 자동/고정 품질 전환 시 시각·음성이 이어지는지를 검증한다.

**Blocked by:** 02 실제 단일 품질 라이브 검증.

**Status:** ready-for-agent

**실행 가능 여부:** 선행 미완료로 blocked. 단일 품질 제품 통합04를 막지는 않지만 ABR 완성을 주장할 수 없다.

- [ ] 같은 source clock·GOP·IDR의 rendition을 만들고 소스보다 큰 해상도로 확대하지 않는다.
- [ ] MediaMTX의 첫 video track 제한에 맞춰 별도 경로를 사용하고 실제 media sequence/timestamp/discontinuity 정렬을 검사한다.
- [ ] master의 대역폭·해상도·CODECS와 상대 URI가 실제 출력과 일치하며 준비된 rendition만 광고한다.
- [ ] native Safari와 hls.js에서 재생 중 품질 상향/하향·대역폭 감소·rendition 실패를 재현한다. 실패 또는 미보유 환경을 명시한다.
- [ ] 독립 경로 master로 안정적 ABR이 안 되면 packager 대안과 비용을 보고하고 Codex 설계 결정 전 임의로 기술을 바꾸지 않는다.
- [ ] 기본 FFmpeg HLS와 LL-HLS를 혼동하지 않고 manifest의 parts/blocking reload를 확인한다.

**검증:** 실영상·실음성·30분 재생과 품질 전환 로그. unit test만으로 이 티켓을 완료할 수 없다.

**결과:** 아직 미실행.

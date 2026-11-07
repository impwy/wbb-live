#!/bin/sh
# Host-side checks against a RUNNING spike (publisher active).
# Verifies: admin API reachable, LL-HLS markers in the MEDIA playlist the
# master points at (redirects followed), and actual codecs/resolution via
# ffprobe inside the container. Every step fails explicitly under set -e
# (no && -list exceptions, no pipe-masked failures).
set -e
cd "$(dirname "$0")"

BASE_HLS=http://127.0.0.1:8888/spike/720p
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

echo "== MediaMTX 관리 API (루프백) =="
# Split into a tempfile step: a curl|python|head pipe would mask upstream
# failures under plain sh (no pipefail).
if ! curl -fsS --max-time 5 http://127.0.0.1:9990/v3/paths/list -o "$TMP/paths.json"; then
  echo "관리 API 조회 실패" >&2
  exit 1
fi
if ! python3 -m json.tool "$TMP/paths.json" > "$TMP/paths.pretty.json"; then
  echo "관리 API 응답이 올바른 JSON이 아님" >&2
  exit 1
fi
head -30 "$TMP/paths.pretty.json"
echo

echo "== LL-HLS: master → variant media playlist (redirects followed) =="
if ! curl -fsSL --max-time 10 "$BASE_HLS/index.m3u8" -o "$TMP/master.m3u8"; then
  echo "master playlist 조회 실패" >&2
  exit 1
fi
VARIANT=$(python3 - "$TMP/master.m3u8" <<'PY'
import sys
for line in open(sys.argv[1]):
    line = line.strip()
    if line and not line.startswith('#'):
        print(line)
        break
PY
)
[ -n "$VARIANT" ] || { echo "master에 variant URI가 없음" >&2; exit 1; }
echo "variant: $VARIANT"
if ! curl -fsSL --max-time 10 "$BASE_HLS/$VARIANT" -o "$TMP/media.m3u8"; then
  echo "media playlist 조회 실패" >&2
  exit 1
fi
head -12 "$TMP/media.m3u8"
echo "..."
# Explicit if/else checks: a `grep -q ... && echo` AND-list would NOT fail
# the script under set -e when the tag is missing.
if grep -q "EXT-X-PART-INF" "$TMP/media.m3u8"; then
  echo "LL-HLS 확인: media playlist EXT-X-PART-INF 존재 ✔"
else
  echo "LL-HLS 실패: media playlist에 EXT-X-PART-INF 없음 ✘" >&2
  exit 1
fi
if grep -q "CAN-BLOCK-RELOAD=YES" "$TMP/media.m3u8"; then
  echo "LL-HLS 확인: CAN-BLOCK-RELOAD=YES ✔"
else
  echo "LL-HLS 실패: CAN-BLOCK-RELOAD=YES 없음 ✘" >&2
  exit 1
fi
echo

echo "== 변환 결과 코덱/해상도 (ffprobe JSON, 컨테이너 내부) =="
if ! docker compose exec -T mediamtx ffprobe -v error -of json \
  -show_streams "$BASE_HLS/index.m3u8" > "$TMP/streams.json"; then
  echo "ffprobe 실행 실패" >&2
  exit 1
fi
python3 - "$TMP/streams.json" <<'PY' || { echo "코덱/해상도 검사 실패" >&2; exit 1; }
import json, sys
streams = json.load(open(sys.argv[1]))['streams']
videos = [s for s in streams if s.get('codec_type') == 'video']
audios = [s for s in streams if s.get('codec_type') == 'audio']
assert videos, 'no video stream'
assert all(s.get('codec_name') == 'h264' for s in videos), \
    f'video must be h264: {[s.get("codec_name") for s in videos]}'
assert audios, 'no audio stream (aac required)'
assert all(s.get('codec_name') == 'aac' for s in audios), \
    f'audio must be aac: {[s.get("codec_name") for s in audios]}'
for s in videos:
    w, h = s.get('width') or 0, s.get('height') or 0
    assert w > 0 and h > 0, f'non-positive dimensions: {w}x{h}'
    assert w <= 1280 and h <= 720, f'exceeds 1280x720 cap: {w}x{h}'
    print(f'video {s["codec_name"]} {w}x{h} OK')
for s in audios:
    print(f'audio {s["codec_name"]} OK')
PY
echo "코덱/해상도 검사 통과 (video h264 ≤1280x720 + audio aac)"

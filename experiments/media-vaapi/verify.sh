#!/bin/sh
# Server-side live checks (a publisher must be active).
# Admin API, LL-HLS markers in the MEDIA playlist, ffprobe JSON assertions
# (h264/aac, positive dims, <=1280x720), and audio non-silence via PCM.
set -e
cd "$(dirname "$0")"

BASE_HLS=http://127.0.0.1:18888/spike/720p
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

echo "== MediaMTX 관리 API =="
if ! curl -fsS --max-time 5 http://127.0.0.1:19990/v3/paths/list -o "$TMP/paths.json"; then
  echo "관리 API 조회 실패" >&2; exit 1
fi
python3 -m json.tool "$TMP/paths.json" > "$TMP/paths.pretty.json" || { echo "관리 API JSON 파싱 실패" >&2; exit 1; }
head -20 "$TMP/paths.pretty.json"
echo

echo "== LL-HLS: master → variant media playlist =="
curl -fsSL --max-time 10 "$BASE_HLS/index.m3u8" -o "$TMP/master.m3u8" || { echo "master 조회 실패" >&2; exit 1; }
VARIANT=$(python3 - "$TMP/master.m3u8" <<'PY'
import sys
for line in open(sys.argv[1]):
    line = line.strip()
    if line and not line.startswith('#'):
        print(line); break
PY
)
[ -n "$VARIANT" ] || { echo "variant URI 없음" >&2; exit 1; }
echo "variant: $VARIANT"
curl -fsSL --max-time 10 "$BASE_HLS/$VARIANT" -o "$TMP/media.m3u8" || { echo "media playlist 조회 실패" >&2; exit 1; }
head -10 "$TMP/media.m3u8"; echo "..."
grep -q "EXT-X-PART-INF" "$TMP/media.m3u8" && echo "LL-HLS: EXT-X-PART-INF ✔" || { echo "PART-INF 없음" >&2; exit 1; }
grep -q "CAN-BLOCK-RELOAD=YES" "$TMP/media.m3u8" && echo "LL-HLS: CAN-BLOCK-RELOAD=YES ✔" || { echo "CAN-BLOCK-RELOAD 없음" >&2; exit 1; }
echo

echo "== 코덱/해상도 (ffprobe JSON; VAAPI 출력) =="
timeout -k 2 25 docker compose exec -T mediamtx timeout 20 ffprobe -v error -of json -show_streams http://127.0.0.1:8888/spike/720p/index.m3u8 > "$TMP/streams.json" \
  || { echo "ffprobe 실패" >&2; exit 1; }
python3 - "$TMP/streams.json" <<'PY' || { echo "코덱/해상도 검사 실패" >&2; exit 1; }
import json, sys
streams = json.load(open(sys.argv[1]))['streams']
videos = [s for s in streams if s.get('codec_type') == 'video']
audios = [s for s in streams if s.get('codec_type') == 'audio']
assert videos and all(s.get('codec_name') == 'h264' for s in videos), f'video: {[s.get("codec_name") for s in videos]}'
assert audios and all(s.get('codec_name') == 'aac' for s in audios), f'audio: {[s.get("codec_name") for s in audios]}'
for s in videos:
    w, h = s.get('width') or 0, s.get('height') or 0
    assert w > 0 and h > 0 and w <= 1280 and h <= 720, f'dims {w}x{h}'
    print(f'video h264 {w}x{h} OK')
for s in audios:
    print('audio aac OK')
PY

echo "== 오디오 비영 (PCM decode) =="
timeout -k 2 25 docker compose exec -T mediamtx timeout 20 ffmpeg -v error \
  -i http://127.0.0.1:8888/spike/720p/index.m3u8 -map 0:a:0 -t 3 -f s16le - > "$TMP/audio.pcm" \
  || { echo "PCM 디코드 실패" >&2; exit 1; }
python3 - "$TMP/audio.pcm" <<'PY' || { echo "오디오 침묵" >&2; exit 1; }
import struct, sys
data = open(sys.argv[1], 'rb').read()
assert len(data) > 1000, f'too short: {len(data)}'
peak = max(abs(x) for x in struct.unpack(f'<{len(data)//2}h', data[:len(data)//2*2]))
assert peak > 0, 'silence'
print(f'PCM {len(data)}B peak={peak} non-silent OK')
PY
echo "verify 통과"

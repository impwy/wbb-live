#!/bin/sh
# Host-side checks against a RUNNING spike (publisher active).
# Verifies: admin API reachable, LL-HLS markers in the manifest, and the
# actual renditions' codecs/resolution via ffprobe inside the container.
set -e
cd "$(dirname "$0")"

MANIFEST=http://127.0.0.1:8888/spike/720p/index.m3u8

echo "== MediaMTX 관리 API (루프백) =="
curl -fsS http://127.0.0.1:9990/v3/paths/list | python3 -m json.tool | head -30
echo

echo "== LL-HLS 매니페스트 =="
curl -fsS --max-time 5 "$MANIFEST" | tee /tmp/spike-manifest.m3u8 | head -15
echo "..."
if grep -q "EXT-X-PART-INF" /tmp/spike-manifest.m3u8; then
  echo "LL-HLS 확인: EXT-X-PART-INF 존재 ✔"
else
  echo "LL-HLS 실패: EXT-X-PART-INF 없음 ✘" >&2
  exit 1
fi
if grep -q "CAN-BLOCK-RELOAD=YES" /tmp/spike-manifest.m3u8; then
  echo "LL-HLS 확인: CAN-BLOCK-RELOAD=YES ✔"
fi
echo

echo "== 변환 결과 코덱/해상도 (ffprobe, 컨테이너 내부) =="
docker compose exec -T mediamtx ffprobe -v error \
  -show_entries stream=index,codec_name,codec_type,width,height,avg_frame_rate \
  -of csv=p=0 "$MANIFEST"
echo "(기대: video=h264 width<=1280, audio=aac)"

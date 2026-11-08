#!/bin/sh
# Synthetic RTSP publisher (server-side phase A): CPU testsrc2+sine -> RTSP.
# This simulates a browser publisher; the GPU path under test is the
# transcoder, not this source.
set -u
W="${SRC_WIDTH:-1280}"
H="${SRC_HEIGHT:-720}"
FPS="${SRC_FPS:-30}"
DST="rtsp://mediamtx:8554/spike/source"

echo "[synth-source] publishing ${W}x${H}@${FPS} to $DST"
exec ffmpeg -nostdin -hide_banner -loglevel warning \
  -re -f lavfi -i "testsrc2=size=${W}x${H}:rate=${FPS}" \
  -re -f lavfi -i "sine=frequency=440:sample_rate=48000" \
  -map 0:v:0 -map 1:a:0 \
  -c:v libx264 -preset ultrafast -tune zerolatency -pix_fmt yuv420p -g 60 -b:v 3000k \
  -c:a aac -b:a 128k -ar 48000 -ac 2 \
  -f rtsp -rtsp_transport tcp "$DST"

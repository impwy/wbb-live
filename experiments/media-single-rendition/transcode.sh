#!/bin/sh
# Spike transcode loop: WHIP source -> H.264/AAC single 720p rendition.
# CPU only (libx264) on the local Mac; Intel VAAPI is a Labtop follow-up.
# Never upscales: scale='min(1280,iw)' keeps smaller sources untouched.
set -u
SRC="rtsp://mediamtx:8554/spike/source"
DST="rtsp://mediamtx:8554/spike/720p"

echo "[transcoder] waiting for WHIP source at $SRC"
while true; do
  ffmpeg -nostdin -hide_banner -loglevel warning \
    -rtsp_transport tcp -i "$SRC" \
    -map 0:v:0 -map 0:a:0? \
    -c:v libx264 -preset veryfast -tune zerolatency -profile:v main -pix_fmt yuv420p \
    -vf "scale='min(1280,iw)':'min(720,ih)':force_original_aspect_ratio=decrease:force_divisible_by=2" \
    -r 30 -g 60 -keyint_min 60 -sc_threshold 0 \
    -b:v 2500k -maxrate 2700k -bufsize 500k \
    -c:a aac -b:a 128k -ar 48000 -ac 2 \
    -f rtsp -rtsp_transport tcp "$DST"
  code=$?
  echo "[transcoder] ffmpeg exited with $code; retrying in 2s"
  sleep 2
done

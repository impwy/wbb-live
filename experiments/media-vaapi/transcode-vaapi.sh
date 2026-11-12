#!/bin/sh
# VAAPI transcode loop: WHIP/RTSP source -> H.264(h264_vaapi)/AAC 720p.
# GPU ENCODE IS MANDATORY: on any failure this retries the SAME VAAPI
# pipeline — CPU substitution is forbidden for this spike.
# Software decode of the browser's VP8/H264 input is allowed by design.
# Both-axis cap, no upscaling, even dimensions (portrait sources too).
set -u
SRC="rtsp://mediamtx:8554/spike/source"
DST="rtsp://mediamtx:8554/spike/720p"
DEV="/dev/dri/renderD128"

echo "[transcoder-vaapi] device check:"
ls -l "$DEV" || exit 1

echo "[transcoder-vaapi] waiting for source at $SRC"
while true; do
  ffmpeg -nostdin -hide_banner -loglevel warning \
    -rtsp_transport tcp -i "$SRC" \
    -map 0:v:0 -map 0:a:0? \
    -init_hw_device vaapi=va:"$DEV" -filter_hw_device va \
    -vf "scale='min(1280,iw)':'min(720,ih)':force_original_aspect_ratio=decrease:force_divisible_by=2,format=nv12,hwupload" \
    -c:v h264_vaapi -profile:v main -bf 0 -g 60 -keyint_min 60 -sc_threshold 0 \
    -b:v 2500k -maxrate 2700k -bufsize 500k -r 30 \
    -af "aresample=async=1:first_pts=0" \
    -c:a aac -b:a 128k -ar 48000 -ac 2 \
    -f rtsp -rtsp_transport tcp "$DST"
  code=$?
  echo "[transcoder-vaapi] ffmpeg exited with $code (VAAPI only, no CPU fallback); retrying in 2s"
  sleep 2
done

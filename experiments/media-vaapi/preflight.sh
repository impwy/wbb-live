#!/bin/sh
# GPU preflight — runs ON the Labtop server inside the spike's transcoder
# image (built from Dockerfile.transcoder). Proves the CONTAINER can use the
# Iris Xe render node: device visible, iHD driver init, real 30-frame
# h264_vaapi encode. Host-side success proves nothing here.
set -e
cd "$(dirname "$0")"

echo "== 이미지/패키지 기록 =="
docker compose build transcoder
docker image inspect wbb-media-vaapi/transcoder:alpine3.24 --format 'transcoder image id: {{.Id}}'
docker inspect alpine:3.24 --format 'base alpine:3.24 digest: {{index .RepoDigests 0}}'
docker run --rm wbb-media-vaapi/transcoder:alpine3.24 -c \
  'apk list --installed 2>/dev/null | grep -E "^(ffmpeg|intel-media-driver|libva|libva-utils)-" | cut -d" " -f1'

echo "== GPU 장치 (컨테이너 내부) =="
docker run --rm --device /dev/dri/renderD128 wbb-media-vaapi/transcoder:alpine3.24 \
  -c 'ls -l /dev/dri/renderD128 && ls /usr/lib/dri/ | head -4'

echo "== VAAPI 드라이버 초기화 (vainfo, drm display) =="
docker run --rm --device /dev/dri/renderD128 wbb-media-vaapi/transcoder:alpine3.24 \
  -c 'LIBVA_DRIVER_NAME=iHD vainfo --display drm --device /dev/dri/renderD128 > /tmp/vainfo.out 2>&1; rc=$?; head -8 /tmp/vainfo.out; echo vainfo_rc=$rc; [ $rc -eq 0 ] || exit 1'

echo "== 실제 30프레임 h264_vaapi 인코딩 =="
docker run --rm --device /dev/dri/renderD128 wbb-media-vaapi/transcoder:alpine3.24 \
  -c 'ffmpeg -v error -init_hw_device vaapi=va:/dev/dri/renderD128 -filter_hw_device va \
    -f lavfi -i testsrc2=size=1280x720:rate=30 \
    -vf format=nv12,hwupload -c:v h264_vaapi -profile:v main -bf 0 -g 60 -b:v 2500k \
    -frames:v 30 -f null - && echo "VAAPI_30FRAME_ENCODE_OK"'

echo "== 참고: 공식 mediamtx 이미지(드라이버 없음) 대조 =="
docker run --rm --device /dev/dri/renderD128 --entrypoint sh \
  bluenviron/mediamtx:1.21.3-ffmpeg@sha256:208e9f1740a09a5ee63b6308e77ce0d54fce0456462f640e6bd9acfc62ebb5a2 \
  -c 'ffmpeg -v error -init_hw_device vaapi=va:/dev/dri/renderD128 -f lavfi -i testsrc2=size=1280x720:rate=30 -vf format=nv12,hwupload -c:v h264_vaapi -frames:v 5 -f null - > /tmp/stock.out 2>&1; rc=$?; head -2 /tmp/stock.out; echo "stock_rc=$rc (드라이버 부재 재현 — 이 이미지는 VAAPI 미동작)"' || true

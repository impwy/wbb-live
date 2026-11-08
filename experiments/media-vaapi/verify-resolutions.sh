#!/bin/sh
# Phase A item 2: three source sizes through the real VAAPI transcoder.
# Expectations (both-axis cap, no upscale, even dims):
#   1280x720 -> 1280x720   (already at cap)
#   1080x1920 -> 406x720   (portrait, height capped)
#   640x360  -> 640x360    (below cap, unchanged)
# Runs ON the server. Each case: restart source with new size, wait, ffprobe.
set -e
cd "$(dirname "$0")"

# 실패 경로 정리: source 컨테이너를 최선으로 제거하고, 제거 실패도 보고한다.
cleanup_on_exit() {
  rc=$1
  if [ $rc -ne 0 ]; then
    if ! docker compose --profile source rm -sf source >/dev/null 2>&1; then
      echo "실패 경로 source 제거도 실패 — 남은 자원:" >&2
      docker compose --profile source ps -a >&2 || true
    fi
  fi
}
trap 'cleanup_on_exit $?' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

check_case() {
  W=$1; H=$2; EW=$3; EH=$4
  echo "== 케이스 ${W}x${H} → 기대 ${EW}x${EH} =="
  docker compose --profile source rm -sf source >/dev/null 2>&1 || true
  SRC_WIDTH=$W SRC_HEIGHT=$H docker compose --profile source up -d --force-recreate source >/dev/null

  # (1) 입력 검증: source path 가 실제로 WxH 인지 (compose env 전달 확인).
  ok=0; i=0
  while [ $i -lt 20 ]; do
    sleep 2; i=$((i + 1))
    in=$(docker compose exec -T mediamtx ffprobe -v error -of csv=p=0 \
      -show_entries stream=codec_name,codec_type,width,height \
      rtsp://127.0.0.1:8554/spike/source 2>/dev/null || true)
    if printf '%s\n' "$in" | grep -q "h264,video,${W},${H}"; then ok=1; break; fi
  done
  if [ "$ok" != "1" ]; then
    echo "입력 치수 불일치 (기대 ${W}x${H}, 마지막: $(echo "$in" | tr '\n' ' '))" >&2
    docker compose --profile source rm -sf source >/dev/null 2>&1 || true; exit 1
  fi
  echo "입력 확인: ${W}x${H} ($(echo "$in" | tr '\n' ' '))"

  # (2) 출력 폴링: 모든 video 라인이 기대 치수 + aac (스테일 세그먼트 혼재 배제).
  ok=0; i=0
  while [ $i -lt 20 ]; do
    sleep 2; i=$((i + 1))
    out=$(docker compose exec -T mediamtx ffprobe -v error -of csv=p=0 \
      -show_entries stream=codec_name,codec_type,width,height \
      http://127.0.0.1:8888/spike/720p/index.m3u8 2>/dev/null || true)
    nv=$(printf '%s\n' "$out" | grep -c "h264,video,${EW},${EH}" || true)
    allv=$(printf '%s\n' "$out" | grep -c "h264,video" || true)
    na=$(printf '%s\n' "$out" | grep -c "aac,audio" || true)
    if [ "$nv" -ge 1 ] && [ "$nv" = "$allv" ] && [ "$na" -ge 1 ]; then
      echo "출력 확인(${i}회차): $(echo "$out" | tr '\n' ' ')"
      ok=1; break
    fi
  done
  if [ "$ok" != "1" ]; then
    echo "출력 치수 불일치 (기대 ${EW}x${EH}, 마지막: $(echo "$out" | tr '\n' ' '))" >&2
    docker compose --profile source rm -sf source >/dev/null 2>&1 || true; exit 1
  fi
}

check_case 1280 720 1280 720
check_case 1080 1920 406 720
check_case 640 360 640 360

# 성공 경로 정리도 검증: 제거 실패는 실패로 보고한다.
if ! docker compose --profile source rm -sf source >/dev/null 2>&1; then
  echo "source 제거 실패 — 남은 자원:" >&2
  docker compose --profile source ps -a >&2 || true
  exit 1
fi
trap - EXIT INT TERM
echo "해상도 3케이스 통과 (양축 제한·무확대·짝수 유지)"

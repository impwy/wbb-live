#!/bin/sh
# Start the VAAPI spike stack on the Labtop server (loopback-only ports).
# Refuses when this compose project already owns containers; on failure it
# tears down ONLY what this project created (no external kills, no prune).
set -e
cd "$(dirname "$0")"

echo "== 기존 프로젝트 확인 (profile 포함) =="
# 조회 실패는 "없음"으로 가정하지 않고 즉시 중단한다(파이프로 rc 가림 금지).
if ! EXISTING=$(docker compose --profile source ps -aq); then
  echo "기존 컨테이너 조회 실패 — 진행 불가" >&2
  exit 1
fi
if [ -n "$EXISTING" ]; then
  echo "wbb-media-vaapi 프로젝트에 기존 컨테이너가 있습니다. ./stop.sh 후 재시도하세요." >&2
  exit 1
fi

echo "== 포트 사전 점검 (루프백) =="
for p in 18554 18888 18889 19990 18390; do
  if ss -ltn "( sport = :$p )" | grep -q LISTEN; then
    echo "TCP 포트 $p 가 이미 사용 중입니다." >&2
    exit 1
  fi
done

# From the first `up` onward, any failure removes ONLY this project's stack.
cleanup() {
  if ! docker compose --profile source down --remove-orphans >/dev/null 2>&1; then
    echo "정리(down) 실패 — 남은 자원:" >&2
    docker compose --profile source ps -a >&2 || true
  fi
}
trap cleanup EXIT INT TERM

docker compose up -d --wait mediamtx
docker compose build transcoder
docker compose up -d transcoder

# Post-start sanity: both containers of THIS project are running.
RUNNING=$(docker compose ps --format '{{.State}}' | grep -c running || true)
[ "$RUNNING" -ge 2 ] || { echo "실행 중 컨테이너 부족($RUNNING)" >&2; exit 1; }

trap - EXIT INT TERM
echo "스택 기동 완료 (HLS 18888 / WHIP 18889 / API 19990 / RTSP 18554 / ICE-TCP 18390, 모두 127.0.0.1)"

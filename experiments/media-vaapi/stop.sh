#!/bin/sh
# Stop the VAAPI spike stack. Compose project resources only — including
# profile-gated containers. Reports failure honestly (no success message or
# rc 0 when `down` fails) and never touches other projects.
set -e
cd "$(dirname "$0")"
if ! docker compose --profile source down --remove-orphans; then
  echo "down 실패 — 남은 자원:" >&2
  docker compose --profile source ps -a >&2 || true
  exit 1
fi
echo "VAAPI 스파이크 스택 정지 완료"

#!/bin/sh
# Start the WBB media spike: MediaMTX + FFmpeg transcoder (docker, loopback
# ports) and the local web UI (Go static server on 127.0.0.1:8090).
set -e
cd "$(dirname "$0")"

echo "== 포트 사전 점검 (루프백) =="
for p in 8090 8888 8889 9990 8554; do
  if lsof -nP -iTCP:$p -sTCP:LISTEN >/dev/null 2>&1; then
    echo "TCP 포트 $p 가 이미 사용 중입니다. 종료 후 재시도하세요." >&2
    exit 1
  fi
done
if lsof -nP -iUDP:18389 >/dev/null 2>&1; then
  echo "UDP 포트 18389 가 이미 사용 중입니다." >&2
  exit 1
fi

echo "== docker 스택 기동 (project: wbb-media-spike) =="
docker compose up -d --wait

echo "== 웹 UI 빌드/기동 (루프백 강제) =="
go build -o bin/spike-server . || exit 1
BIN="$PWD/bin/spike-server"
nohup "$BIN" > .spike-server.log 2>&1 &
PID=$!
# 바인드 실패로 즉시 죽은 경우 stale pid를 남기지 않는다.
sleep 0.5
if ! kill -0 "$PID" 2>/dev/null; then
  echo "웹 서버 기동 실패:"; tail -3 .spike-server.log; exit 1
fi
echo "$PID" > .spike-server.pid

# 실제 readiness 확인(프로세스 생존 + HTTP 200).
ready=0
for i in $(seq 1 30); do
  if ! kill -0 "$(cat .spike-server.pid)" 2>/dev/null; then
    echo "웹 서버가 종료됨:"; tail -3 .spike-server.log; exit 1
  fi
  if curl -fsS -o /dev/null http://127.0.0.1:8090/ 2>/dev/null; then ready=1; break; fi
  sleep 0.5
done
[ "$ready" = "1" ] || { echo "웹 UI readiness 실패"; tail -3 .spike-server.log; exit 1; }
# 포트 점유 프로세스가 실제로 우리 서버인지 재확인(오래된 프로세스 충돌 방지).
LPID=$(lsof -nP -tiTCP:8090 -sTCP:LISTEN | head -1)
LCMD=$(ps -p "$LPID" -o command= 2>/dev/null || true)
case "$LCMD" in
  "$BIN"*) echo "웹 UI ready (http://127.0.0.1:8090, pid $LPID)" ;;
  *) echo "경고: 8090 점유 프로세스($LCMD)가 이 스파이크 서버가 아님"; exit 1 ;;
esac

cat <<EOF

기동 완료:
  웹 UI(송출+시청+로그):  http://127.0.0.1:8090
  LL-HLS 매니페스트:      http://127.0.0.1:8888/spike/720p/index.m3u8
  WHIP 시그널링:          http://127.0.0.1:8889/spike/source/whip
  MediaMTX 관리 API:      http://127.0.0.1:9990 (루프백 전용)
정지: ./stop.sh
검증: 송출 중인 상태에서 ./verify.sh
EOF

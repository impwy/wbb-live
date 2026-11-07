#!/bin/sh
# Stop the WBB media spike. Only kills the process we started: the PID file
# must match a process whose command line begins with THIS directory's
# absolute spike-server path. No pattern matching, no unrelated kills.
cd "$(dirname "$0")"
EXPECTED="$PWD/bin/spike-server"

if [ -f .spike-server.pid ]; then
  pid=$(cat .spike-server.pid)
  if kill -0 "$pid" 2>/dev/null; then
    cmd=$(ps -p "$pid" -o command= 2>/dev/null)
    case "$cmd" in
      "$EXPECTED"*)
        kill "$pid" 2>/dev/null || true
        echo "웹 서버 정지 (pid $pid, $cmd)"
        ;;
      *)
        echo "경고: pid $pid($cmd)는 이 스파이크의 $EXPECTED 프로세스가 아님 — 종료하지 않음"
        ;;
    esac
  else
    echo "PID 파일이 오래됨(프로세스 없음) — 정리"
  fi
  rm -f .spike-server.pid
fi

# 스파이크 서버가 여전히 8090을 점유하고 있다면(소유 검증 후) 종료한다.
LPID=$(lsof -nP -tiTCP:8090 -sTCP:LISTEN 2>/dev/null | head -1)
if [ -n "$LPID" ]; then
  LCMD=$(ps -p "$LPID" -o command= 2>/dev/null || true)
  case "$LCMD" in
    "$EXPECTED"*)
      kill "$LPID" 2>/dev/null || true
      echo "잔여 웹 서버 정지 (pid $LPID, $LCMD)"
      ;;
    *) echo "경고: 8090 점유($LCMD)는 이 스파이크 서버가 아님 — 건드리지 않음" ;;
  esac
fi

docker compose down
echo "스파이크 스택 정지 완료 (이미지는 유지)"

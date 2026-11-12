#!/bin/sh
# Helper for the wbb-live stack. Usage: ./scripts.sh <init-secrets|bootstrap|up|down|logs|status>
set -eu
cd "$(dirname "$0")"
DC="docker compose --env-file .env -f compose.yml"
# WBB_STANDALONE=1 (or in .env) adds the TLS front for servers without Caddy.
if [ "${WBB_STANDALONE:-}" = 1 ] || grep -q '^WBB_STANDALONE=1' .env 2>/dev/null; then
  DC="$DC -f compose.standalone.yml"
fi
case "${1:-}" in
init-secrets)
  # Creates .env with fresh random secrets if it does not exist yet.
  [ -e .env ] && { echo ".env already exists; not overwriting" >&2; exit 1; }
  umask 077
  rnd() { head -c "$1" /dev/urandom | od -An -tx1 | tr -d ' \n'; }
  {
    echo "WBB_PUBLIC_ORIGIN=https://live.wombatburrow.com"
    echo "WBB_PUBLIC_HOST=live.wombatburrow.com"
    echo "WBB_STANDALONE=0"
    echo "WBB_DB_PASSWORD=$(rnd 24)"
    echo "WBB_JWT_SECRET=$(rnd 32)"
    echo "WBB_MEDIA_SECRET=$(rnd 32)"
    echo "WBB_COMPLETION_KEY_HEX=$(rnd 32)"
    echo "WBB_COMPLETION_ROOT=wbb-live-$(rnd 8)"
    echo "WBB_COMPLETION_SLOT=slot-$(rnd 8)"
    echo "WBB_ICE_HOSTS=<public-ip>,<lan-ip>"
    echo "WBB_ICE_PORT=18391"
    echo "WBB_EDGE_PORT=19480"
    echo "WBB_WEB_DIR=./web"
    echo "WBB_EVAL_LOGIN_ENABLED=false"
    echo "WBB_EVAL_LOGIN_EMAIL="
    echo "WBB_EVAL_LOGIN_PASSWORD="
    echo "WBB_TURN_KEY_ID="
    echo "WBB_TURN_API_TOKEN="
  } > .env
  echo "wrote .env (edit WBB_ICE_HOSTS)";;
bootstrap)
  # One-time: creates the empty completion-history volume and its anchor.
  . ./.env
  if docker volume inspect wbb-live_completion-state >/dev/null 2>&1; then
    echo "completion volume already exists; bootstrap must run only once" >&2; exit 1
  fi
  umask 077
  printf '{"root":"%s","slot":"%s"}\n' "$WBB_COMPLETION_ROOT" "$WBB_COMPLETION_SLOT" > anchor.json
  chmod 644 anchor.json
  $DC build
  docker volume create wbb-live_completion-state >/dev/null
  $DC run --rm --no-deps --entrypoint /wbb-bootstrap mediamtx /dev/null
  echo "bootstrap done";;
up) $DC up -d --build;;
down) $DC down;;
logs) shift; $DC logs --tail=200 "$@";;
status) $DC ps;;
*) echo "usage: $0 <init-secrets|bootstrap|up|down|logs|status>" >&2; exit 2;;
esac

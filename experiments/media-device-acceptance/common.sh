#!/bin/sh
# Source this from scripts after cd into this experiment directory.
load_settings() {
  assignments=$(python3 ./settings.py shell) || return 1
  eval "$assignments"
  export WBB_MEDIA_AUTH_USER WBB_MEDIA_AUTH_HASH WBB_MEDIA_ORIGIN
  export WBB_MEDIA_HOSTNAME WBB_MEDIA_HTTPS_PORT WBB_MEDIA_BIND_IP
  export WBB_MEDIA_ICE_HOST WBB_MEDIA_ICE_PORT WBB_MEDIA_ENCODER
}

# Explicit project/config/env file on EVERY call. Inherited Compose settings
# and media values cannot redirect `down` to another project or replace .env.
compose() {
  env -u COMPOSE_FILE -u COMPOSE_PROJECT_NAME -u COMPOSE_PROFILES \
    -u WBB_MEDIA_AUTH_USER -u WBB_MEDIA_AUTH_HASH -u WBB_MEDIA_ORIGIN \
    -u WBB_MEDIA_HOSTNAME -u WBB_MEDIA_HTTPS_PORT -u WBB_MEDIA_BIND_IP \
    -u WBB_MEDIA_ICE_HOST -u WBB_MEDIA_ICE_PORT -u WBB_MEDIA_ENCODER \
    -u WBB_MEDIA_WEB_DIR \
    docker compose --project-directory "$PWD" --env-file "$PWD/.env" \
    -p wbb-media-acceptance -f "$PWD/docker-compose.yml" "$@"
}

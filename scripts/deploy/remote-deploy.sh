#!/bin/sh

set -eu

APP_DIR="${APP_DIR:-}"
IMAGE_TAG="${IMAGE_TAG:-}"
DRY_RUN="${DRY_RUN:-0}"
REGISTRY_HOST="${REGISTRY_HOST:-}"
REGISTRY_USERNAME="${REGISTRY_USERNAME:-}"
REGISTRY_PASSWORD="${REGISTRY_PASSWORD:-}"

if [ -z "$APP_DIR" ]; then
  echo "APP_DIR is required" >&2
  exit 1
fi

if [ -z "$IMAGE_TAG" ]; then
  echo "IMAGE_TAG is required" >&2
  exit 1
fi

COMPOSE_FILE="$APP_DIR/docker-compose.prod.yml"
ENV_FILE="$APP_DIR/.env"

if [ ! -f "$COMPOSE_FILE" ]; then
  echo "Missing compose file: $COMPOSE_FILE" >&2
  exit 1
fi

if [ ! -f "$ENV_FILE" ]; then
  echo "Missing env file: $ENV_FILE" >&2
  exit 1
fi

upsert_image_tag() {
  if grep -q '^IMAGE_TAG=' "$ENV_FILE"; then
    sed -i "s/^IMAGE_TAG=.*/IMAGE_TAG=$IMAGE_TAG/" "$ENV_FILE"
  else
    printf '\nIMAGE_TAG=%s\n' "$IMAGE_TAG" >> "$ENV_FILE"
  fi
}

env_value() {
  key="$1"
  awk -F= -v key="$key" '$1 == key { sub(/\r$/, "", $2); print $2; exit }' "$ENV_FILE"
}

upsert_image_tag

if [ "$DRY_RUN" = "1" ]; then
  echo "DRY_RUN=1"
  echo "Would deploy IMAGE_TAG=$IMAGE_TAG in APP_DIR=$APP_DIR"
  exit 0
fi

if [ -n "$REGISTRY_HOST" ] || [ -n "$REGISTRY_USERNAME" ] || [ -n "$REGISTRY_PASSWORD" ]; then
  if [ -z "$REGISTRY_HOST" ] || [ -z "$REGISTRY_USERNAME" ] || [ -z "$REGISTRY_PASSWORD" ]; then
    echo "REGISTRY_HOST, REGISTRY_USERNAME, and REGISTRY_PASSWORD must all be set together" >&2
    exit 1
  fi

  printf '%s' "$REGISTRY_PASSWORD" | docker login "$REGISTRY_HOST" -u "$REGISTRY_USERNAME" --password-stdin
else
  echo "Registry credentials not provided; using existing docker auth state"
fi

docker compose -f "$COMPOSE_FILE" --env-file "$ENV_FILE" pull
docker compose -f "$COMPOSE_FILE" --env-file "$ENV_FILE" up -d
docker compose -f "$COMPOSE_FILE" --env-file "$ENV_FILE" ps

SERVER_PORT_VALUE="$(env_value SERVER_PORT)"
if [ -z "$SERVER_PORT_VALUE" ]; then
  echo "SERVER_PORT is required in $ENV_FILE" >&2
  exit 1
fi

curl -fsS "http://127.0.0.1:${SERVER_PORT_VALUE}/healthz"

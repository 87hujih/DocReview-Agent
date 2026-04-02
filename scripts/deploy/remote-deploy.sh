#!/bin/sh

set -eu

APP_DIR="${APP_DIR:-}"
IMAGE_TAG="${IMAGE_TAG:-}"
DRY_RUN="${DRY_RUN:-0}"
GHCR_USERNAME="${GHCR_USERNAME:-}"
GHCR_TOKEN="${GHCR_TOKEN:-}"
SERVER_IMAGE_ARCHIVE="${SERVER_IMAGE_ARCHIVE:-}"
WEB_IMAGE_ARCHIVE="${WEB_IMAGE_ARCHIVE:-}"

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

if grep -q '^IMAGE_TAG=' "$ENV_FILE"; then
  sed -i "s/^IMAGE_TAG=.*/IMAGE_TAG=$IMAGE_TAG/" "$ENV_FILE"
else
  printf '\nIMAGE_TAG=%s\n' "$IMAGE_TAG" >> "$ENV_FILE"
fi

if [ "$DRY_RUN" = "1" ]; then
  echo "DRY_RUN=1"
  echo "Would deploy IMAGE_TAG=$IMAGE_TAG in APP_DIR=$APP_DIR"
  exit 0
fi

load_image_archive() {
  archive_path="$1"
  image_label="$2"

  if [ ! -f "$archive_path" ]; then
    echo "Missing image archive for $image_label: $archive_path" >&2
    exit 1
  fi

  docker load -i "$archive_path"
}

if [ -n "$SERVER_IMAGE_ARCHIVE" ] || [ -n "$WEB_IMAGE_ARCHIVE" ]; then
  if [ -z "$SERVER_IMAGE_ARCHIVE" ] || [ -z "$WEB_IMAGE_ARCHIVE" ]; then
    echo "SERVER_IMAGE_ARCHIVE and WEB_IMAGE_ARCHIVE must both be set when using archive deployment" >&2
    exit 1
  fi

  load_image_archive "$SERVER_IMAGE_ARCHIVE" "server"
  load_image_archive "$WEB_IMAGE_ARCHIVE" "web"
else
  if [ -z "$GHCR_USERNAME" ] || [ -z "$GHCR_TOKEN" ]; then
    echo "GHCR_USERNAME and GHCR_TOKEN are required for GHCR-based deployments" >&2
    exit 1
  fi

  printf '%s' "$GHCR_TOKEN" | docker login ghcr.io -u "$GHCR_USERNAME" --password-stdin
  docker compose -f "$COMPOSE_FILE" --env-file "$ENV_FILE" pull
fi

docker compose -f "$COMPOSE_FILE" --env-file "$ENV_FILE" up -d
docker compose -f "$COMPOSE_FILE" --env-file "$ENV_FILE" ps

if [ -n "$SERVER_IMAGE_ARCHIVE" ] && [ -n "$WEB_IMAGE_ARCHIVE" ]; then
  rm -f "$SERVER_IMAGE_ARCHIVE" "$WEB_IMAGE_ARCHIVE"
fi

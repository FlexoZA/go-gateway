#!/bin/bash

# Build & publish script for device-gateway.
#
# Unlike the old single-image gateway, this project ships TWO images:
#   - the gateway  (deploy/Dockerfile, UNIT=howen)  -> flexoza/go-gateway
#   - the admin UI (admin/Dockerfile)               -> flexoza/go-gateway-admin
#
# This script:
#   1) Requires a version tag.
#   2) Syncs the repo with origin/main (only if the working tree is clean).
#   3) Builds both images, tagged <tag> and latest.
#   4) Logs in to Docker Hub (DOCKER_USER/DOCKER_TOKEN from local.env, or prompts).
#   5) Pushes both images.
#   6) (Optional, commented) SSHes to the server and rolls the compose stack.
#
# Usage:
#   ./update-server.sh 2026.06.22.1

set -e

# ---- Configuration ----
GATEWAY_IMAGE="flexoza/go-gateway"
ADMIN_IMAGE="flexoza/go-gateway-admin"
PUSH_LATEST="${PUSH_LATEST:-true}"   # also tag/push :latest

# Server deploy (used by the commented section at the bottom)
SSH_TARGET="gw1"
SERVER_DIR="~/go-gateway"

cd "$(dirname "$0")"

# Load DOCKER_USER / DOCKER_TOKEN from local.env if present (gitignored).
if [ -f "./local.env" ]; then
  set -a
  # shellcheck disable=SC1091
  . "./local.env"
  set +a
fi

# ---- Tag ----
TAG_INPUT="$1"
if [ -z "${TAG_INPUT}" ]; then
  echo "ERROR: You must provide a version tag."
  echo "Usage:"
  echo "  $0 22.06.2026.1"
  exit 1
fi
IMAGE_TAG="${TAG_INPUT}"

GATEWAY_TAGGED="${GATEWAY_IMAGE}:${IMAGE_TAG}"
ADMIN_TAGGED="${ADMIN_IMAGE}:${IMAGE_TAG}"
NAMESPACE="${GATEWAY_IMAGE%%/*}"   # Docker Hub account the images must be pushed under

# ---- Docker Hub login (fail fast, before building) ----
echo "========================================"
echo "Docker Hub authentication"
echo "========================================"
CURRENT_USER="$(docker info 2>/dev/null | awk -F': ' '/Username/{print $2}' | tr -d '[:space:]')"

if [ -n "${DOCKER_USER}" ] && [ -n "${DOCKER_TOKEN}" ]; then
  # Non-interactive creds provided — (re)login if the active user differs.
  if [ "${CURRENT_USER}" != "${DOCKER_USER}" ]; then
    echo "Logging in as ${DOCKER_USER} (from local.env)..."
    echo "${DOCKER_TOKEN}" | docker login -u "${DOCKER_USER}" --password-stdin
    CURRENT_USER="${DOCKER_USER}"
  fi
elif [ -z "${CURRENT_USER}" ]; then
  echo "Not logged in. Running docker login..."
  docker login
  CURRENT_USER="$(docker info 2>/dev/null | awk -F': ' '/Username/{print $2}' | tr -d '[:space:]')"
fi

if [ -z "${CURRENT_USER}" ]; then
  echo "ERROR: Not logged in to Docker Hub."
  exit 1
fi
if [ "${CURRENT_USER}" != "${NAMESPACE}" ]; then
  echo "ERROR: Logged in as '${CURRENT_USER}', but images target the '${NAMESPACE}' namespace."
  echo "       Docker Hub would reject the push (denied). Do one of:"
  echo "         - docker login -u ${NAMESPACE}     # then re-run"
  echo "         - put DOCKER_USER=${NAMESPACE} + a write token in local.env"
  echo "         - or set GATEWAY_IMAGE/ADMIN_IMAGE to the '${CURRENT_USER}' namespace"
  exit 1
fi
echo "✅ Authenticated as '${CURRENT_USER}' — matches target namespace '${NAMESPACE}'"
echo ""

# ---- Repo sync ----
echo "========================================"
echo "Checking for updates on main"
echo "========================================"
if [ -n "$(git status --porcelain)" ]; then
  echo "⚠️  Working tree has uncommitted changes — skipping git pull (building from local state)."
else
  git fetch origin main
  if [ "$(git rev-parse HEAD)" != "$(git rev-parse origin/main)" ]; then
    echo "Local branch is behind origin/main. Pulling latest changes..."
    git pull origin main
    echo "✅ Repository updated"
  else
    echo "✅ Repository is up to date"
  fi
fi
echo ""

# ---- Build ----
echo "========================================"
echo "Building images (version: ${IMAGE_TAG})"
echo "========================================"

echo "Building gateway: ${GATEWAY_TAGGED}"
docker build -f deploy/Dockerfile --build-arg UNIT=howen -t "${GATEWAY_TAGGED}" .
echo "✅ Gateway built"
echo ""

echo "Building admin panel: ${ADMIN_TAGGED}"
docker build -t "${ADMIN_TAGGED}" admin
echo "✅ Admin built"
echo ""

if [ "${PUSH_LATEST}" = "true" ]; then
  docker tag "${GATEWAY_TAGGED}" "${GATEWAY_IMAGE}:latest"
  docker tag "${ADMIN_TAGGED}" "${ADMIN_IMAGE}:latest"
  echo "✅ Also tagged :latest"
  echo ""
fi

# ---- Push ----
echo "========================================"
echo "Pushing images to Docker Hub"
echo "========================================"
docker push "${GATEWAY_TAGGED}"
docker push "${ADMIN_TAGGED}"
if [ "${PUSH_LATEST}" = "true" ]; then
  docker push "${GATEWAY_IMAGE}:latest"
  docker push "${ADMIN_IMAGE}:latest"
fi
echo "✅ Pushed:"
echo "   ${GATEWAY_TAGGED}"
echo "   ${ADMIN_TAGGED}"
echo ""

# ---- Server roll-out (optional) ----
# Requires deploy/docker-compose.yml on the server to reference the Hub images,
# e.g.  image: ${GATEWAY_IMAGE}:${IMAGE_TAG:-latest}  /  ${ADMIN_IMAGE}:${IMAGE_TAG:-latest}
#
# echo "Deploying to ${SSH_TARGET}:${SERVER_DIR} ..."
# ssh "${SSH_TARGET}" "cd ${SERVER_DIR} && IMAGE_TAG=${IMAGE_TAG} docker compose pull && IMAGE_TAG=${IMAGE_TAG} docker compose up -d"
# echo "✅ Server updated"

echo "Done. Deployed tag: ${IMAGE_TAG}"

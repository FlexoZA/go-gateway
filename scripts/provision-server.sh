#!/usr/bin/env bash
# Provision a server for an EXISTING unit type (one unit per server). This is an
# OPERATOR task — the unit's Go code must already exist. To author a brand-new
# protocol's code first, use scripts/new-gateway.sh.
#
#   scripts/provision-server.sh <unit> [--no-build]
#
# It:
#   1. builds the lean image device-gateway-<unit> (only that unit compiles in),
#   2. writes deploy/docker-compose.<unit>.yml — a full stack (Postgres + the unit
#      gateway + admin) derived from the canonical compose,
#   3. writes deploy/.env from deploy/.env.example if it doesn't exist yet.
#
# Then: edit deploy/.env (secrets, webhook, MEDIA_ADVERTISE_HOST) and run
#   docker compose -f deploy/docker-compose.<unit>.yml --env-file deploy/.env up -d --build
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
UNIT="${1:-}"
BUILD=1
[[ "${2:-}" == "--no-build" ]] && BUILD=0

if [[ -z "$UNIT" ]]; then
  echo "usage: scripts/provision-server.sh <unit> [--no-build]" >&2
  exit 1
fi
if [[ ! "$UNIT" =~ ^[a-z][a-z0-9_]*$ ]]; then
  echo "error: unit name must be lowercase [a-z0-9_], starting with a letter" >&2
  exit 1
fi
# Provisioning != scaffolding: the unit's code must already exist.
if [[ ! -d "$ROOT/cmd/$UNIT" ]]; then
  echo "error: cmd/$UNIT not found — this unit has no code yet." >&2
  echo "       Scaffold it first:  scripts/new-gateway.sh $UNIT" >&2
  exit 1
fi

COMPOSE_OUT="$ROOT/deploy/docker-compose.$UNIT.yml"
ENV_OUT="$ROOT/deploy/.env"

# Per-unit compose: the canonical stack with the howen service renamed to <unit>.
# `howen` only appears as the service/image/container name, the UNIT build-arg, and
# the admin's depends_on/GATEWAY_URL, so a straight substitution yields a valid,
# runnable single-unit stack (ports unchanged — one unit per server/host).
if [[ "$UNIT" == "howen" ]]; then
  echo "note: 'howen' already has deploy/docker-compose.yml — using that."
elif [[ -e "$COMPOSE_OUT" ]]; then
  echo "keeping existing $COMPOSE_OUT"
else
  sed "s/howen/$UNIT/g" "$ROOT/deploy/docker-compose.yml" > "$COMPOSE_OUT"
  echo "wrote deploy/docker-compose.$UNIT.yml"
fi

if [[ -e "$ENV_OUT" ]]; then
  echo "keeping existing deploy/.env"
elif [[ -e "$ROOT/deploy/.env.example" ]]; then
  cp "$ROOT/deploy/.env.example" "$ENV_OUT"
  echo "wrote deploy/.env (from .env.example — fill in secrets before running)"
fi

if [[ "$BUILD" == "1" ]]; then
  echo "building image device-gateway-$UNIT ..."
  docker build -f "$ROOT/deploy/Dockerfile" --build-arg UNIT="$UNIT" \
    -t "device-gateway-$UNIT" "$ROOT"
fi

COMPOSE_FILE="deploy/docker-compose.yml"
[[ "$UNIT" != "howen" ]] && COMPOSE_FILE="deploy/docker-compose.$UNIT.yml"
cat <<EOF

Provisioned '$UNIT'. Next:
  1. Edit deploy/.env — ADMIN_API_TOKEN, SESSION_SECRET, DEVICE_WEBHOOK_URL,
     and (only for video units) MEDIA_ADVERTISE_HOST.
  2. Start the stack:
       docker compose -f $COMPOSE_FILE --env-file deploy/.env up -d --build
EOF

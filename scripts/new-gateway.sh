#!/usr/bin/env bash
# Scaffold a new unit-type gateway server from the gps-only template.
#
#   scripts/new-gateway.sh <unit-name>
#
# Creates:
#   internal/<unit>/protocol.go   (Protocol + Session skeleton — edit this)
#   cmd/<unit>/main.go            (entrypoint, wired to the framework)
#
# Then implement ReadFrame + parsing in internal/<unit>/protocol.go, build with
#   docker build -f deploy/Dockerfile --build-arg UNIT=<unit> -t device-gateway-<unit> .
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
UNIT="${1:-}"

if [[ -z "$UNIT" ]]; then
  echo "usage: scripts/new-gateway.sh <unit-name>" >&2
  exit 1
fi
if [[ ! "$UNIT" =~ ^[a-z][a-z0-9_]*$ ]]; then
  echo "error: unit name must be lowercase, start with a letter, and contain only [a-z0-9_]" >&2
  exit 1
fi

INTERNAL_DIR="$ROOT/internal/$UNIT"
CMD_DIR="$ROOT/cmd/$UNIT"
TPL="$ROOT/templates/gps-only"

if [[ -e "$INTERNAL_DIR" || -e "$CMD_DIR" ]]; then
  echo "error: internal/$UNIT or cmd/$UNIT already exists" >&2
  exit 1
fi

mkdir -p "$INTERNAL_DIR" "$CMD_DIR"
sed "s/__UNIT__/$UNIT/g" "$TPL/internal/protocol.go.tmpl" > "$INTERNAL_DIR/protocol.go"
sed "s/__UNIT__/$UNIT/g" "$TPL/cmd/main.go.tmpl" > "$CMD_DIR/main.go"

cat <<EOF
Created:
  internal/$UNIT/protocol.go
  cmd/$UNIT/main.go

Next steps:
  1. Implement ReadFrame + parsing in internal/$UNIT/protocol.go
  2. Build & test:
       docker build -f deploy/Dockerfile --build-arg UNIT=$UNIT -t device-gateway-$UNIT .
  3. Add a service to deploy/docker-compose.yml (copy the howen service, set UNIT=$UNIT and a unique LISTEN_PORT/published port).
EOF

#!/usr/bin/env bash
# Build and (re)deploy the self-hosted stack from this checkout.
#
#   ./deploy.sh            # backend + frontend
#   ./deploy.sh backend    # backend only (skips the ~10min web build)
#   ./deploy.sh frontend
#
# Isolated from the ~/.multica/server install: own project name, own env file,
# own volumes, own ports. Never use bare `docker compose` or `make selfhost*`
# on this checkout - both default to project `multica` and would recreate that
# install's containers and reuse its pgdata volume.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")"

COMPOSE="docker compose -p multica-local --env-file .env.selfhost"
export COMPOSE

# ponytail: unquoted on purpose - service names never contain spaces, and this
# avoids empty-array + `set -u` blowing up on macOS bash 3.2.
services="${*:-backend frontend}"

$COMPOSE -f docker-compose.selfhost.yml -f docker-compose.selfhost.build.yml \
  up -d --build $services

bash scripts/selfhost-wait.sh build

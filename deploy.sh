#!/usr/bin/env bash
# Build and (re)deploy the self-hosted stack from this checkout.
#
#   ./deploy.sh            # backend only (desktop app is the client, not the web frontend)
#   ./deploy.sh frontend   # opt back into the dockerized web frontend
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
services="${*:-backend}"

$COMPOSE -f docker-compose.selfhost.yml -f docker-compose.selfhost.build.yml \
  up -d --build $services

bash scripts/selfhost-wait.sh build

# The host-side CLI (agent runtimes run on this Mac, not in a container) is
# separate from the backend/frontend images above, so a docker-only redeploy
# would leave it on stale code. Rebuild it for manual use in the same step
# whenever backend code is part of this deploy. The daemon itself ships
# inside the desktop app bundle (its own compile below) and restarts itself
# when it detects the bundled binary changed - no separate daemon here.
case " $services " in
*" backend "*)
  echo "Rebuilding CLI from source..."
  make build

  echo "Rebuilding desktop app from source..."
  CSC_IDENTITY_AUTO_DISCOVERY=false pnpm --filter @multica/desktop package
  app_bundle="apps/desktop/dist/mac-arm64/Multica.app"
  if [ -d "$app_bundle" ]; then
    osascript -e 'quit app "Multica"' 2>/dev/null || true
    rm -rf "apps/desktop/dist/Multica.app.old"
    if [ -d /Applications/Multica.app ]; then
      mv /Applications/Multica.app apps/desktop/dist/Multica.app.old
    fi
    ditto "$app_bundle" /Applications/Multica.app
    xattr -dr com.apple.quarantine /Applications/Multica.app 2>/dev/null || true
    rm -rf "apps/desktop/dist/Multica.app.old"
  fi
  ;;
esac

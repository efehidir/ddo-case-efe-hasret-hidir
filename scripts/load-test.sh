#!/usr/bin/env sh
set -eu
mkdir -p evidence
export LOCAL_UID="$(id -u):$(id -g)"
running=$(docker compose ps --status running --services simulator)
restore() { if [ -n "$running" ]; then docker compose start simulator >/dev/null; fi; }
trap restore EXIT INT TERM
docker compose stop simulator
# The internal Compose network works on Linux and Docker Desktop alike.
docker compose --profile load run --rm load

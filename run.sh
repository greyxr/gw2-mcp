#!/bin/sh
set -eu

project_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
if [ -z "${GW2_API_KEY:-}" ] && [ -f "$project_dir/.env" ]; then
    set -a
    . "$project_dir/.env"
    set +a
fi

exec "$project_dir/bin/gw2-mcp" "$@"

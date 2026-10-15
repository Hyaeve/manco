#!/usr/bin/env sh
set -eu

if [ "$#" -ne 2 ]; then
    echo "usage: prepare-upstream.sh <kototoro-root> <mihon-host-root>" >&2
    exit 2
fi

UPSTREAM_ROOT=$1
HOST_ROOT=$2
MODULE_ROOT="$UPSTREAM_ROOT/manco-mihon-host"

rm -rf "$MODULE_ROOT"
mkdir -p "$MODULE_ROOT"
cp "$HOST_ROOT/build.gradle" "$MODULE_ROOT/build.gradle"
cp -R "$HOST_ROOT/src" "$MODULE_ROOT/src"

if ! grep -q "include('manco-mihon-host')" "$UPSTREAM_ROOT/settings.gradle"; then
    printf "\ninclude('manco-mihon-host')\n" >> "$UPSTREAM_ROOT/settings.gradle"
fi

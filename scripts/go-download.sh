#!/usr/bin/env bash
# Downloads the module's dependencies (and wails, if WAILS_VERSION is set)
# with retries: proxy.golang.org downloads failed two releases in a row, and
# each time re-running the job was all it took. Later steps then find
# everything in the module cache.
set -u
for i in 1 2 3 4; do
  if go mod download && { [ -z "${WAILS_VERSION:-}" ] || go install "github.com/wailsapp/wails/v2/cmd/wails@$WAILS_VERSION"; }; then
    exit 0
  fi
  echo "go-download: attempt $i failed; retrying in $((i * 20)) s" >&2
  sleep $((i * 20))
done
echo "go-download: giving up after 4 attempts" >&2
exit 1

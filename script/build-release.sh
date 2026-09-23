#!/usr/bin/env bash
set -euo pipefail

tag="${1:-}"
if [[ -z "$tag" ]]; then
  echo "usage: script/build-release.sh <release-tag>" >&2
  exit 2
fi

mkdir -p dist
rm -f dist/*

targets=(
  "linux amd64"
  "linux arm64"
  "darwin amd64"
  "darwin arm64"
  "windows amd64 .exe"
  "windows arm64 .exe"
)

for target in "${targets[@]}"; do
  read -r goos goarch ext <<< "$target"
  output="dist/gh-shoal_${tag}_${goos}-${goarch}${ext:-}"
  echo "building ${output}"
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build \
    -trimpath \
    -ldflags "-s -w -X main.version=${tag}" \
    -o "$output" \
    ./cmd/gh-shoal
done

#!/usr/bin/env bash
set -euo pipefail

workflow=".github/workflows/release.yml"

if [[ ! -f "$workflow" ]]; then
  echo "missing release workflow: $workflow" >&2
  exit 1
fi

required_lines=(
  "push:"
  "tags:"
  '"v*"'
  "contents: write"
  "id-token: write"
  "attestations: write"
  "uses: cli/gh-extension-precompile@v2.2.0"
  "go_version_file: go.mod"
  "build_script_override: script/build-release.sh"
  "generate_attestations: true"
)

for required_line in "${required_lines[@]}"; do
  if ! grep -Fq "$required_line" "$workflow"; then
    echo "release workflow is missing required configuration: $required_line" >&2
    exit 1
  fi
done

for forbidden_trigger in "pull_request:" "workflow_dispatch:"; do
  if grep -Fq "$forbidden_trigger" "$workflow"; then
    echo "release workflow must not be a PR-stage or manual-dispatch gate: $forbidden_trigger" >&2
    exit 1
  fi
done

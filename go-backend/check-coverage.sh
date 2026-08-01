#!/bin/sh
set -eu

minimum_coverage="${MDHUB_MIN_COVERAGE:-70.0}"
coverage_profile="$(mktemp "${TMPDIR:-/tmp}/mdhub-coverage.XXXXXX")"
trap 'rm -f "$coverage_profile"' EXIT

go test -count=1 -coverprofile="$coverage_profile" ./...
actual_coverage="$(go tool cover -func="$coverage_profile" | awk '/^total:/ { gsub("%", "", $3); print $3 }')"

if ! awk -v actual="$actual_coverage" -v minimum="$minimum_coverage" 'BEGIN { exit !(actual + 0 >= minimum + 0) }'; then
  echo "coverage ${actual_coverage}% is below required ${minimum_coverage}%" >&2
  exit 1
fi

echo "coverage gate passed: ${actual_coverage}% >= ${minimum_coverage}%"

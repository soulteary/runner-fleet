#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."
if [[ $(uname -s) != Linux ]]; then
  echo 'Coverage validation requires Linux; use a Linux container.' >&2
  exit 1
fi

bash scripts/test-coverage-gate.sh

coverage_profile=${COVERAGE_PROFILE:-.coverage/coverage.out}
test_report=${TEST_REPORT:-.coverage/tests.jsonl}
mkdir -p "$(dirname "$coverage_profile")" "$(dirname "$test_report")"

go test -count=1 -p 1 -race -covermode=atomic \
  -coverprofile="$coverage_profile" -json ./... | tee "$test_report"
awk -f scripts/check-go-coverage.awk "$coverage_profile"

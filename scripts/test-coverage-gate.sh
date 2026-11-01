#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
coverage_gate_tmp=$(mktemp -d)
trap 'rm -rf "$coverage_gate_tmp"' EXIT

check_profile() {
  local expected=$1
  local profile=$2
  local status=0
  awk -f scripts/check-go-coverage.awk "$profile" > "$coverage_gate_tmp/result" 2>&1 || status=$?
  if [[ $status != "$expected" ]]; then
    cat "$coverage_gate_tmp/result" >&2
    echo "coverage gate returned $status; expected $expected" >&2
    exit 1
  fi
}

printf 'mode: atomic\nfixture.go:1.1,2.1 95 1\nfixture.go:3.1,4.1 5 0\n' > "$coverage_gate_tmp/exact"
check_profile 0 "$coverage_gate_tmp/exact"
printf 'mode: atomic\nfixture.go:1.1,2.1 9496 1\nfixture.go:3.1,4.1 504 0\n' > "$coverage_gate_tmp/rounded"
check_profile 1 "$coverage_gate_tmp/rounded"
cat "$coverage_gate_tmp/exact" > "$coverage_gate_tmp/duplicate"
printf 'fixture.go:1.1,2.1 95 0\n' >> "$coverage_gate_tmp/duplicate"
check_profile 0 "$coverage_gate_tmp/duplicate"
printf 'fixture.go:1.1,2.1 96 1\n' >> "$coverage_gate_tmp/duplicate"
check_profile 2 "$coverage_gate_tmp/duplicate"
printf 'mode: atomic\n' > "$coverage_gate_tmp/empty"
check_profile 2 "$coverage_gate_tmp/empty"
printf 'not a profile\n' > "$coverage_gate_tmp/invalid"
check_profile 2 "$coverage_gate_tmp/invalid"
echo 'coverage gate boundary checks passed'

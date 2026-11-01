#!/usr/bin/env bash
set -euo pipefail
file=${1:?Dockerfile required}
binary=${2:?binary required}
task_tmp=$(mktemp -d)
trap 'rm -rf -- "$task_tmp"' EXIT
cp "$file" "$task_tmp/Dockerfile"
printf '\nFROM scratch AS architecture-check\nCOPY --from=builder /app/%s /%s\n' "$binary" "$binary" >> "$task_tmp/Dockerfile"
docker buildx build --platform linux/amd64,linux/arm64 --target architecture-check \
  --output "type=local,dest=$task_tmp/output" -f "$task_tmp/Dockerfile" .
for platform in amd64 arm64; do
  case "$platform" in amd64) expected=62 ;; arm64) expected=183 ;; esac
  actual=$(od -An -tu2 -j18 -N2 "$task_tmp/output/linux_$platform/$binary" | tr -d '[:space:]')
  if [[ "$actual" != "$expected" ]]; then
    echo "$file linux/$platform: ELF machine $actual, expected $expected" >&2
    exit 1
  fi
  echo "$file linux/$platform: ELF machine $actual verified"
done

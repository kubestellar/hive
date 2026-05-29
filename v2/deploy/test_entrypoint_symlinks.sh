#!/usr/bin/env bash
# Tests that the entrypoint creates the required symlinks for
# Copilot CLI token persistence across container restarts.
# Run: bash v2/deploy/test_entrypoint_symlinks.sh
set -euo pipefail

PASS=0
FAIL=0

assert_contains() {
  local file="$1" pattern="$2" label="$3"
  if grep -q "$pattern" "$file"; then
    echo "  PASS: $label"
    PASS=$((PASS + 1))
  else
    echo "  FAIL: $label"
    echo "        pattern '$pattern' not found in $file"
    FAIL=$((FAIL + 1))
  fi
}

ENTRYPOINT="$(cd "$(dirname "$0")" && pwd)/entrypoint.sh"

echo "=== Entrypoint symlink regression tests ==="

# 1. ~/.copilot symlink to /data/home/.copilot must exist in entrypoint
assert_contains "$ENTRYPOINT" \
  'ln -sfn /data/home/.copilot /home/dev/.copilot' \
  "~/.copilot -> /data/home/.copilot symlink"

# 2. mkdir must create /data/home/.copilot
assert_contains "$ENTRYPOINT" \
  '/data/home/.copilot' \
  "/data/home/.copilot directory referenced"

# 3. ~/.config/github-copilot symlink must still exist (pre-existing)
assert_contains "$ENTRYPOINT" \
  'ln -sfn /data/config/github-copilot /home/dev/.config/github-copilot' \
  "~/.config/github-copilot -> /data/config/github-copilot symlink"

# 4. /data/home/.config/github-copilot symlink must still exist (pre-existing)
assert_contains "$ENTRYPOINT" \
  'ln -sfn /data/config/github-copilot /data/home/.config/github-copilot' \
  "/data/home/.config/github-copilot -> /data/config/github-copilot symlink"

# 5. group-writable chmod on /data/home (ensures all agent UIDs can access)
assert_contains "$ENTRYPOINT" \
  'chmod -R g+rwX /data/home' \
  "/data/home is group-writable"

echo ""
echo "Results: $PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ] || exit 1

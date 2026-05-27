#!/usr/bin/env bash
# setup-proxy-iptables.sh — Force all GitHub HTTPS traffic through the ACMM proxy.
# Run inside the container as root during startup. This makes the proxy
# un-bypassable even if an agent unsets HTTPS_PROXY.
set -euo pipefail

PROXY_PORT=18443

for host in api.github.com github.com; do
  for ip in $(dig +short "$host" 2>/dev/null); do
    iptables -t nat -A OUTPUT -p tcp -d "$ip" --dport 443 \
      -m owner ! --uid-owner root \
      -j REDIRECT --to-port "$PROXY_PORT" 2>/dev/null || true
    echo "[iptables] $host ($ip) → localhost:$PROXY_PORT"
  done
done

echo "[iptables] GitHub proxy redirect rules installed"

#!/usr/bin/env bash
# ST-12: runs the image hardened (read-only root filesystem, all capabilities
# dropped, no-new-privileges, non-root) and checks health, headers, the UID,
# the absence of a shell, idle memory, and refusal of a loose secrets file.
# Usage: scripts/container-test.sh <image>
set -euo pipefail
cd "$(dirname "$0")/.."

image=${1:?usage: container-test.sh <image>}
name="nexus-ct-$$"
bad="nexus-ct-bad-$$"
cleanup() { docker rm -f "$name" "$bad" >/dev/null 2>&1 || true; }
trap cleanup EXIT

as_root() { if [ "$(id -u)" -eq 0 ]; then "$@"; else sudo "$@"; fi; }
fail() { echo "FAIL: $*" >&2; docker logs "$name" 2>&1 | tail -20 >&2 || true; exit 1; }

work=$(mktemp -d "${TMPDIR:-/tmp}/nexus-ct.XXXXXX")
mkdir -p "$work/etc" "$work/data"
cp examples/catalog.yaml "$work/etc/catalog.yaml"
# Port-forwarded requests arrive from the bridge gateway: trust it for this test.
gateway=$(docker network inspect bridge -f '{{(index .IPAM.Config 0).Gateway}}')
cat > "$work/etc/config.yaml" <<YAML
listen: 0.0.0.0:8480
publicOrigin: https://nexus.example.ts.net
trustedProxies: [${gateway}]
requireTailnet: false
dataDir: /var/lib/nexus
catalogPath: /etc/nexus/catalog.yaml
secretsFile: /etc/nexus/secrets.yaml
YAML
printf 'sessionEncryptionKey: %s\n' "$(head -c 32 /dev/urandom | base64)" > "$work/etc/secrets.yaml"
chmod 0644 "$work/etc/config.yaml" "$work/etc/catalog.yaml"
chmod 0600 "$work/etc/secrets.yaml"
chmod 0700 "$work/data"
as_root chown 65532:65532 "$work/etc/secrets.yaml" "$work/data"

hardening=(--read-only --cap-drop=ALL --security-opt no-new-privileges:true --user 65532:65532)

echo "== hardened run"
docker run -d --name "$name" "${hardening[@]}" \
  --health-interval=1s --health-start-period=2s \
  -v "$work/etc:/etc/nexus:ro" -v "$work/data:/var/lib/nexus" \
  -p 127.0.0.1::8480 "$image" >/dev/null

for _ in $(seq 1 60); do
  status=$(docker inspect -f '{{.State.Health.Status}}' "$name")
  [ "$status" = healthy ] && break
  [ "$(docker inspect -f '{{.State.Running}}' "$name")" = true ] || fail "container exited"
  sleep 0.5
done
[ "$status" = healthy ] || fail "health status: $status"
echo "healthcheck: healthy"

[ "$(docker inspect -f '{{.HostConfig.ReadonlyRootfs}}' "$name")" = true ] || fail "root filesystem is not read-only"
[ "$(docker inspect -f '{{.HostConfig.CapDrop}}' "$name")" = "[ALL]" ] || fail "capabilities not dropped"

uid=$(docker top "$name" -eo pid,uid | awk 'NR==2 {print $2}')
[ -n "$uid" ] && [ "$uid" != 0 ] || fail "process UID is '$uid'"
echo "process uid: $uid"

if docker exec "$name" /bin/sh -c true >/dev/null 2>&1; then fail "image contains a shell"; fi
echo "no shell: ok"

port=$(docker port "$name" 8480/tcp | head -1 | sed 's/.*://')
body=$(curl -fsS "http://127.0.0.1:${port}/healthz")
[ "$body" = '{"status":"ok"}' ] || fail "healthz body: $body"
headers=$(curl -fsS -D - -o /dev/null "http://127.0.0.1:${port}/")
grep -qi '^content-security-policy: .*frame-ancestors' <<<"$headers" || fail "CSP header missing"
if grep -qi "unsafe-inline" <<<"$headers"; then fail "CSP contains unsafe-inline"; fi
grep -qi '^x-content-type-options: nosniff' <<<"$headers" || fail "nosniff missing"
echo "http: ok"

as_root test -f "$work/data/nexus.db" || fail "database not created in the data volume"
mode=$(as_root stat -c '%a' "$work/data/nexus.db")
[ "$mode" = 600 ] || fail "database mode $mode"
echo "database mode: $mode"

mem=$(docker stats --no-stream --format '{{.MemUsage}}' "$name" | awk '{print $1}')
mib=$(awk -v m="$mem" 'BEGIN { v = m + 0; if (m ~ /GiB/) v *= 1024; else if (m ~ /KiB/) v /= 1024; else if (m ~ /B$/ && m !~ /iB/) v /= 1048576; print v }')
awk -v v="$mib" 'BEGIN { exit !(v < 60) }' || fail "idle memory ${mem} is not under 60 MiB"
echo "idle memory: ${mem}"

echo "== loose secrets file must be refused"
as_root chmod 0644 "$work/etc/secrets.yaml"
if docker run --name "$bad" "${hardening[@]}" \
     -v "$work/etc:/etc/nexus:ro" -v "$work/data:/var/lib/nexus" "$image" >/dev/null 2>"$work/bad.log"; then
  fail "started with a world-readable secrets file"
fi
grep -q "secrets file" "$work/bad.log" || docker logs "$bad" 2>&1 | grep -q "secrets file" || fail "no secrets-file error reported"
echo "loose secrets refused: ok"

echo "container test passed"

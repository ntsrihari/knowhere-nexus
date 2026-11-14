#!/usr/bin/env bash
# Starts bin/nexus with a throwaway config in a fresh temporary directory and
# runs the browser CSP check against it. The temporary directory is left in
# place for inspection (it holds only a generated key and an empty database).
set -euo pipefail
cd "$(dirname "$0")/.."

work=$(mktemp -d "${TMPDIR:-/tmp}/nexus-e2e.XXXXXX")
chmod 700 "$work"
port=${E2E_PORT:-18480}
pid=""
stop() { if [ -n "$pid" ]; then kill "$pid" 2>/dev/null || true; fi; }
trap stop EXIT

key=$(head -c 32 /dev/urandom | base64)
printf 'sessionEncryptionKey: %s\n' "$key" > "$work/secrets.yaml"
chmod 600 "$work/secrets.yaml"
cat > "$work/config.yaml" <<YAML
listen: 127.0.0.1:${port}
publicOrigin: http://localhost:${port}
dataDir: ${work}/data
catalogPath: $(pwd)/examples/catalog.yaml
secretsFile: ${work}/secrets.yaml
YAML

./bin/nexus serve -config "$work/config.yaml" 2> "$work/nexus.log" &
pid=$!
for _ in $(seq 1 50); do
  if ./bin/nexus healthcheck -addr "127.0.0.1:${port}" 2>/dev/null; then break; fi
  sleep 0.1
done
./bin/nexus healthcheck -addr "127.0.0.1:${port}"

if ! (cd frontend && node e2e/csp-check.mjs "http://127.0.0.1:${port}"); then
  echo "--- nexus log (${work}/nexus.log) ---"
  cat "$work/nexus.log"
  exit 1
fi
echo "e2e: temporary files in ${work}"

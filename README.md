# Knowhere Nexus

Nexus is the dashboard and API at `knowhere/`, and the central authorization and execution boundary of the Knowhere home-server platform. It is a single Go binary with a SvelteKit web interface embedded, reachable only over Tailscale.

**Status: M1 (foundation).** This build serves `/healthz` and a placeholder page. Sign-in, widgets, the launcher and theming arrive in later milestones. See [`docs/TASKS.md`](docs/TASKS.md).

## Documents

| File | Contents |
|---|---|
| [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) | Requirements, components, data model, existing Knowhere infrastructure |
| [docs/API_SPEC.md](docs/API_SPEC.md) | `/api/v1` contract |
| [docs/SECURITY.md](docs/SECURITY.md) | Threat model and controls |
| [docs/CONTRACTS.md](docs/CONTRACTS.md) | Versioned catalog and theme contracts (schemas in [`contracts/`](contracts)) |
| [docs/DECISIONS.md](docs/DECISIONS.md) | Owner decisions, recommendation register, open questions |
| [docs/TASKS.md](docs/TASKS.md) | Milestones, progress, test plan |

The existing Knowhere infrastructure is documented in the public, read-only reference repository [`ntsrihari/knowhere-homelab-archive`](https://github.com/ntsrihari/knowhere-homelab-archive).

## Requirements

- Go (the toolchain in `go.mod` is fetched automatically)
- Node.js ≥ 22.17 and npm
- Docker (for the image and container test)

## Common tasks

```bash
make frontend-deps     # npm ci in frontend/
make check             # gofmt, go vet, staticcheck, race tests, svelte-check, Vitest
make frontend build    # build the SvelteKit app, embed it, build bin/nexus
make e2e               # run bin/nexus and a Chromium check for CSP violations
make fuzz              # 20 s fuzzing of each contract parser
make vuln              # govulncheck and npm audit (needs network access to vuln.go.dev)
make image             # docker build -t knowhere-nexus:local
make container-test    # hardened run of the image (read-only, no capabilities, non-root)
```

## Running locally

```bash
make frontend-deps frontend build
mkdir -p ~/.nexus-dev && chmod 700 ~/.nexus-dev
printf 'sessionEncryptionKey: %s\n' "$(head -c 32 /dev/urandom | base64)" > ~/.nexus-dev/secrets.yaml
chmod 600 ~/.nexus-dev/secrets.yaml
cat > ~/.nexus-dev/config.yaml <<EOF
listen: 127.0.0.1:8480
publicOrigin: http://localhost:8480
dataDir: $HOME/.nexus-dev/data
catalogPath: $PWD/examples/catalog.yaml
secretsFile: $HOME/.nexus-dev/secrets.yaml
EOF
./bin/nexus serve -config ~/.nexus-dev/config.yaml
```

Open <http://localhost:8480>. For frontend work, run `cd frontend && npm run dev`: Vite proxies `/api` and `/healthz` to the server on port 8480.

## Configuration

- `config.yaml` (non-secret, strict): see [`examples/config.yaml`](examples/config.yaml).
- Secrets file (mode 0600/0400, owned by the Nexus user): see [`examples/secrets.example.yaml`](examples/secrets.example.yaml). Never commit it.
- Catalog: [`examples/catalog.yaml`](examples/catalog.yaml).

Nexus refuses to start on an unknown config key, an invalid value, a secrets file readable by group or others, or a data directory that isn't private. A missing or invalid catalog doesn't stop it: it starts with an empty catalog and logs why.

## Container

The image runs as UID 65532 on distroless (no shell) and needs only:

- `/etc/nexus` (read-only): `config.yaml`, `catalog.yaml`, `secrets.yaml`
- `/var/lib/nexus` (read-write, mode 0700, owned by 65532): the SQLite database

Run it with `--read-only --cap-drop=ALL --security-opt no-new-privileges:true` and publish no host port. **Deployment to Knowhere is not part of M1 and needs the owner's approval** (docs/DECISIONS.md, B-10).

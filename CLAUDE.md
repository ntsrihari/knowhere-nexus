# CLAUDE.md — Knowhere Nexus

Nexus is the dashboard and API at `knowhere/`, part of the Knowhere platform. The product spec is the **Knowhere Platform Plan** doc. Engineering documents live in `docs/`:

| File | Purpose |
|---|---|
| docs/ARCHITECTURE.md | Requirements (with sources), components, data model, dependencies |
| docs/API_SPEC.md | The `/api/v1` contract used by the web UI, Milano and Collector |
| docs/SECURITY.md | Threat model, sensitive operations, controls |
| docs/TASKS.md | Phased task list and test plan |
| docs/DECISIONS.md | Owner-approved decisions, recommendation register, open questions |
| docs/CONTRACTS.md | Versioned catalog and theme contracts |

## Current state
**M1 approved (A-25), in progress on `feat/m1-foundation`.** Work beyond M1 needs separate approval. Owner-approved decisions are tagged [OWNER] in DECISIONS.md; target repo is `ntsrihari/knowhere-nexus` (A-13).

## Rules for working on Nexus
1. **Don't invent requirements.** Every feature must trace to the Platform Plan or the brief (IDs F-xx, NF-xx, S-xx). Anything else is a recommendation, marked **[REC]**, and needs approval.
2. **Open decisions block their tasks.** Don't pick an answer to a C-xx or D-xx item yourself. Ask.
3. **Don't touch production infrastructure.** No changes on knowhere: Compose files, Caddy, Tailscale, the socket proxy, a host agent, mounts, drives. Tasks marked **[INFRA]** need the owner's explicit go-ahead each time.
4. **Assume nothing about the infrastructure.** Service URLs, mounts, container names and hostnames come from config, never from hardcoded values. The authoritative reference for what exists on Knowhere is the public `ntsrihari/knowhere-homelab-archive` repo (A-24). It is **read-only**: never modify it without explicit approval, never commit secrets anywhere, and report any real credentials found in it separately. Its public visibility is intentional.
5. **Never write, log or return secrets.** Use the redacting secret type. Never commit `secrets*.yaml`.
6. **Default-deny authorization.** Every new route needs an entry in the authz table and in the authz table test.
7. **Docker stays allowlisted.** No create, exec, volume, image or network calls without an approved decision.
8. **No format, delete-volume or remove-container features, ever** (S-09).

## Stack (specified, plus the proposed libraries pending approval)
- Go backend (single binary) — S-01. Proposed (not yet approved, see the register): Go 1.23+ standard-library net/http, slog, modernc SQLite.
- SvelteKit, `adapter-static`, embedded in the binary — S-02 / P-03.
- Jellyfin auth; Jellyfin admin = Nexus admin — S-03, S-04.

## Commands
```bash
make frontend-deps        # once: npm ci in frontend/
make check                # gofmt + vet + staticcheck + go test -race + svelte-check + Vitest
make frontend build       # embed the SvelteKit build and build bin/nexus
make e2e                  # browser check: no CSP violations or console errors
make fuzz                 # contract parser fuzzing (ST-8)
make image container-test # build the image and run the hardened container test (ST-12)
```
- Every test package's `TestMain` blocks outbound network (ST-11). Use `testutil.LocalClient()` for servers under test.
- New routes go through `authz.Registry.Handle` with a role, and must be added to `TestRouteTable` in `internal/app` (ST-1).
- In this development sandbox, Docker build stages can't verify TLS through the egress proxy, and `vuln.go.dev` is blocked. See the M1 notes in `docs/TASKS.md`.

## Related components (separate repos; don't implement them here)
Collector (store), Milano (Android app), Prism (themes). See ARCHITECTURE.md §7 for the interfaces.

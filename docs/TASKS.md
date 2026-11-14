# Nexus — Tasks

Revision 4 (14 Nov 2026). **M1 in progress.** Status column: ⏳ in progress · ✅ done (tested and committed).
Tags: **[OWNER]** owner-approved · **[SPEC]** · **[REC]** pending approval · **[INFRA]** changes knowhere or the tailnet and needs explicit owner approval each time.

## Gate 0 — before any code
| # | Item | Status |
|---|---|---|
| G-1 | Architecture decisions A-01 to A-17 | ✅ Approved |
| G-2 | Phase 1 plan, in principle (A-18) | ✅ Approved |
| G-3 | Catalog contract (A-20) and theme contract (A-21) | ✅ Approved → CONTRACTS.md |
| G-4 | M1 register items: P-01, P-02, P-03, P-07, P-12, P-15, R-17 | ✅ Delegated within M1 scope by A-25 |
| G-5 | `ntsrihari/knowhere-nexus` created and attached; planning docs copied; **the planning branch stays until the copy is verified** (A-19) | ✅ Copied and verified by SHA-256; source branch untouched |
| G-6 | **Explicit approval to start M1** | ✅ Approved (A-25) |

## Gate D — before Phase 1 deployment (code can proceed without these)
| # | Item |
|---|---|
| D-G1 | B-06 Routing and TLS strategy **[INFRA]** |
| D-G2 | B-07 Authorized reverse-proxy routes for Phase 1 **[INFRA]** |
| D-G3 | B-08 Tailscale ACL, and the identity-header binding choice **[INFRA]** |
| D-G4 | B-09 Host mounts for Nexus **[INFRA]** |
| D-G5 | B-10 Deploy docker-socket-proxy (read-only), Nexus and the Caddy routes **[INFRA]** |

---

## Phase 1 — revised implementation plan

Phase 1 delivers: Jellyfin sign-in with 7-day revocable sessions; a Home screen with a launcher from the server catalog; system, storage and service health for everyone, with detail for admins; a household theme; an audit log; and graceful degradation. Docker and the host are read-only.

### M1 — Skeleton, contracts and CI
**Progress:** ✅ M1-01 · ✅ M1-02 · ✅ M1-03 · ✅ M1-04 · ✅ M1-05 · ✅ M1-06 · ✅ M1-07 · ✅ M1-08 · ✅ M1-09 · ✅ M1-10 · ✅ M1-11 · ✅ M1-12 · ✅ M1-13 · ✅ M1-14 · ✅ M1-15 — **M1 complete.** CI run #8 at `74cf646` was green on every job; publishing is tag-only and was skipped.

M1 builds the base every later milestone relies on. **It has no authentication, no integrations and no host or Docker access.** The only route is `/healthz`, plus the static SPA.

| # | Task | Depends on |
|---|---|---|
| M1-01 | **Repo bootstrap.** Copy the planning docs from `JellyWatch@ccr-cf0a3197-23ukny/knowhere-nexus/` to `knowhere-nexus` (`docs/`, with `CLAUDE.md` at the root). Verify by checksum that every file matches. The source branch is left untouched (A-19). Add `.gitignore` covering `secrets*`, `*.db`, `node_modules`, `build/` | G-5 |
| M1-02 | **Go module and layout.** `cmd/nexus`, `internal/...` as in ARCHITECTURE §4.1 (empty packages for later milestones aren't created). Version injected at build time. `nexus serve` and `nexus healthcheck` subcommands | P-01 |
| M1-03 | **Configuration.** Strict `config.yaml` decoding (unknown keys → error). Fields for M1: `listen`, `publicOrigin`, `trustedProxies` (parsed and validated, used from M2), `dataDir`, `catalogPath`. Invalid config → exit with a non-zero code and a clear message | P-15 |
| M1-04 | **Secrets.** A secrets-file loader that refuses permissions broader than 0600 or a different owner UID. A `Secret` type that redacts through `String`, `GoString`, `MarshalJSON`, `MarshalText` and `slog.LogValuer`. M1 holds a single placeholder secret (the R-14 encryption key), loaded but unused | P-07 |
| M1-05 | **HTTP server.** Standard-library `ServeMux`. Timeouts: `ReadHeaderTimeout` 5 s, `ReadTimeout` 15 s, `WriteTimeout` 30 s, `IdleTimeout` 60 s. `MaxHeaderBytes` 16 KB; bodies capped at 1 MB. Graceful shutdown | P-01 |
| M1-06 | **Middleware (M1 subset).** Panic recovery (generic 500, stack trace only in logs), request id, security headers (SECURITY.md §4.7), structured access log with redaction, JSON error envelope | P-15 |
| M1-07 | **Default-deny authorization.** Route registration requires a role (`public`, `user`, `admin`, `admin!`, `admin!+reauth`, `service:<scope>`). Registering a route without one panics. At startup every mux route is checked against the table, and the process refuses to start on a mismatch. Only `public` is reachable in M1, since auth arrives in M2. The `/api/` catch-all returns a 404 envelope | P-15 |
| M1-08 | **SQLite store.** `modernc.org/sqlite`, WAL, `busy_timeout`, `foreign_keys=ON`. DB file created 0600. Forward-only, versioned, idempotent migrations. **Migration 1 creates only `schema_migrations`.** Session and audit tables come with M2 | P-02 |
| M1-09 | **Contracts.** JSON Schema files for `knowhere.catalog` 0.1.0 and `knowhere.theme` 0.1.0 in `contracts/`. Go validators: version rules (R-17), size caps, a YAML decoder that rejects anchors, aliases and duplicate keys, regex checks, and the catalog `revision` rule. Valid and invalid fixtures. Example `catalog.yaml` and default theme | R-17, A-20, A-21 |
| M1-10 | **Catalog loader at startup.** Load and validate the catalog file. If it's missing or invalid, start with an empty catalog and log the reason (never crash, per A-17). The `/api/v1/apps` endpoint comes in M4 | M1-09 |
| M1-11 | **Frontend scaffold.** SvelteKit with `adapter-static` and an SPA fallback. `kit.csp` in hash mode, so there's no `'unsafe-inline'`. TypeScript strict, `svelte-check`, Vitest. A placeholder page only. Fonts self-hosted, no CDN | P-03 |
| M1-12 | **Static serving.** Embedded `embed.FS`. SPA fallback to `index.html` for non-`/api` GET requests. Long-lived immutable caching for hashed assets, `no-cache` for `index.html`. No directory listings. Path cleaning | P-03 |
| M1-13 | **Container image.** Multi-stage build to `gcr.io/distroless/static:nonroot`, `CGO_ENABLED=0`, pinned by digest. Runs with a read-only root filesystem and `/var/lib/nexus` as the only writable volume. `HEALTHCHECK` uses `nexus healthcheck` | — |
| M1-14 | **CI (GitHub Actions).** `go vet`, `staticcheck`, `go test -race`, `govulncheck`, `npm ci`, `svelte-check`, Vitest, `npm audit --audit-level=high`, gitleaks, image build. Push to GHCR **only on tags**. Actions pinned by commit SHA. Dependabot for Go, npm and Actions | — |
| M1-15 | **Developer docs.** Fill in the `CLAUDE.md` commands section; a README with the local run steps (fake config, no real services) | — |

#### M1 implementation notes (decisions made within A-25)
- **Go toolchain 1.27.4** (`go 1.27.0`, `toolchain go1.27.4`). Go 1.24 is past end of support. 1.27.4 is a patch release from 2 Nov 2026, adopted despite the two-week rule because it carries fixes. Analysis tools run with `GOTOOLCHAIN` set to the project toolchain.
- **govulncheck can't run in the development sandbox** (`vuln.go.dev` is blocked by its network policy). It runs in CI.
- **Container (M1-13):** image 24.8 MB; idle memory about 7.5 MiB in the hardened run. In the development sandbox the build stages can't verify TLS through the sandbox's egress proxy, so local verification used a derived Dockerfile (kept outside the repo) that adds that CA to the build stages only. The final image layers were confirmed identical in content (no sandbox CA). CI builds the real `Dockerfile` unchanged.
- **Independent review (M1):** a subagent review found no critical issues and six confirmed findings, all fixed:
  1. Newer-minor contract validation relaxed required fields and matched keys case-insensitively. Documents are now pruned to exact-case known fields, then validated strictly.
  2. A `?`, `#` or `%` in `dataDir` could redirect the SQLite file outside the data directory. The DSN is now URL-escaped, and these characters are rejected in config paths.
  3. CI granted `packages: write` to every image build. Publishing is now a separate job, run only for `v*` tags.
  4. The effective header limit was about 20 KiB. It is now exactly 16 KiB, and the test brackets it.
  5. The CSP is now snapshot-tested as a whole.
  6. A static gate stops unreviewed outbound-network code outside allowlisted files, complementing the runtime guard.
- **SvelteKit 3** (3.1.0) is used. Kit 3 takes its configuration through the `sveltekit()` Vite plugin (no `svelte.config.js`) and replaces `$lib` with Node subpath imports (`#lib/...ts`).
- **npm `legacy-peer-deps`** is set in `frontend/.npmrc`. npm 10's resolver crashes on this dependency set with `Cannot read properties of null (reading 'edgesOut')`. Every peer is pinned explicitly instead.
- **CSP hashes are computed in Go, not with `kit.csp`.** SvelteKit's SPA fallback page has an inline bootstrap script. Go hashes the inline scripts of the embedded `index.html` at startup and adds them to the `script-src` header, so the header stays the single source of the policy and has no `'unsafe-inline'`.
- **`style-src-attr` hash for SvelteKit's route announcer.** The browser check found that SvelteKit renders a fixed inline `style` attribute on its screen-reader live region. Rather than allow `'unsafe-inline'`, the CSP allows exactly that string by hash with `'unsafe-hashes'`. A unit test pins the hash to the one Chromium reported.
- **No web fonts in M1.** A system font stack is used; bundled fonts arrive with the theme runtime (M5, Q-06).
- **Known npm advisories at M1, none applicable and none high:** GHSA-jf9x-2fg2-hx4w (adapter-static: Vercel route config only); GHSA-mw6w-2fmv-pvw3 (kit: adapter-node `reroute` only); GHSA-gcqx-w48m-w2h4 (deepmerge via vite-plugin-svelte, build-time, no fix). The fixed Kit and adapter versions are under two weeks old; Dependabot will propose them. CI fails on high or critical.

#### M1 acceptance criteria
1. `nexus serve` with a valid config: `GET /healthz` → `200 {"status":"ok"}`; `GET /` → the SPA; `GET /api/anything` → a `404` JSON envelope.
2. A route registered without an authz entry → the process refuses to start (covered by a test).
3. Unknown config key, invalid `trustedProxies` entry or bad `publicOrigin` → non-zero exit and a readable error. No secret values in the output.
4. A secrets file that is group- or world-readable, or owned by another UID → refused at startup.
5. Every response (200, 404, 500, static asset) carries the headers in SECURITY.md §4.7. The CSP has no `'unsafe-inline'`.
6. The SPA loads in Playwright with **zero CSP violations** in the console.
7. Catalog and theme validators accept every valid fixture and reject every invalid one, including: missing or unsupported `schemaVersion`, unknown field at a known version, oversized document, YAML alias or anchor, duplicate key, bad id or colour. An invalid catalog at startup → an empty catalog, not a crash.
8. The DB file and data directory are created with modes 0600 and 0700. Migrations are idempotent; running twice is a no-op.
9. The container runs as non-root with `--read-only` and `--cap-drop=ALL`; the healthcheck passes; the image has no shell.
10. Idle RSS under 60 MB (spec NF-01) and a static binary.
11. CI is green: lint, race tests, `govulncheck` (no reachable vulnerabilities), `npm audit` (no high or critical), gitleaks clean.
12. No outbound network connection is made by Nexus during the test suite (asserted by a test transport that fails any dial).

#### Security tests required before M2 starts
| # | Test |
|---|---|
| ST-1 | **Default deny:** a table test of every registered route against the authz table; a startup failure test for a missing entry |
| ST-2 | **Headers:** security headers present on every status code and route type; a CSP snapshot test |
| ST-3 | **Secret redaction:** `Secret` through `fmt` (`%v`, `%+v`, `%#v`), `json`, `slog` (text and JSON) and error wrapping → always `[redacted]`. Startup and request logs captured with real secret values loaded, and scanned for them |
| ST-4 | **Secrets file permissions:** 0600 accepted; 0640, 0644 and wrong owner rejected |
| ST-5 | **Panic handling:** a handler panic → generic 500 envelope, no stack trace or internals in the body |
| ST-6 | **Resource limits:** a body over 1 MB → 413; headers over 16 KB rejected; slow-header client cut off at `ReadHeaderTimeout` |
| ST-7 | **Static traversal:** `../`, `%2e%2e/`, double-encoded, backslash and absolute-path requests never escape `embed.FS`; no directory listings; correct `Content-Type` with `nosniff` |
| ST-8 | **Contract fuzzing:** `go test -fuzz` on the catalog and theme parsers (no panics; caps respected); a billion-laughs YAML fixture rejected |
| ST-9 | **Strict config:** unknown keys and type mismatches fail; `trustedProxies` accepts only single-host CIDRs |
| ST-10 | **File modes:** DB file 0600, data directory 0700 |
| ST-11 | **No outbound network:** a dial-failing transport across the whole test suite |
| ST-12 | **Container:** CI runs the image with `--read-only --cap-drop=ALL --user 65532` and checks that `healthz` succeeds and the process UID is not 0 |
| ST-13 | **Supply chain:** `govulncheck`, `npm audit`, gitleaks and Actions pinned by SHA, enforced as required checks |

### M2 — Auth, sessions, revocation, audit [OWNER A-10, A-14, A-15, A-16]
- [ ] T-2.1 Jellyfin login client: AuthenticateByName and Quick Connect with a per-login device id; read role; **immediately** `/Sessions/Logout` (R-10, *Q-01*)
- [ ] T-2.2 Session families: 15-minute access, rotating refresh with reuse detection, 7-day absolute cap; atomic rotation, 20 s grace, Web Locks single-flight (R-14, *Q-04*)
- [ ] T-2.3 Cookies (`__Host-`/`__Secure-`), CSRF (Origin plus header, login included), bearer-overrides-cookie rule (R-16)
- [ ] T-2.4 Admin window: role from login or re-auth, honoured for 12 h (*Q-02*); re-auth endpoint
- [ ] T-2.5 Revocation: own sessions, admin revoke, revoke-all with re-auth; synchronous cache eviction; SSE close (R-16)
- [ ] T-2.6 Audit writer: hash chain, fail-closed admin actions, list and verify endpoints
- [ ] T-2.7 Rate limiting; trusted-peer enforcement, XFF parsing, Tailscale IP-range check, recording `Tailscale-User-Login` (R-15, *Q-07*)
- [ ] T-2.8 Login, Quick Connect and session-management pages

### M3 — Read-only metrics and failure isolation [OWNER A-03, A-04, A-07, A-08, A-17]
- [ ] T-3.1 `resilience` package: timeouts, size caps, bulkheads, circuit breakers
- [ ] T-3.2 Widget framework: pollers, cache, `ok`/`stale`/`unavailable`/`not_configured`, per-widget panic isolation
- [ ] T-3.3 Host reader: `/host/proc` and `/host/sys` (CPU, RAM, load, temperature, uptime)
- [ ] T-3.4 Storage: mountinfo parsing, configured mount list, internal-hdd exclusion, user summary and admin detail; free space only if B-09 allows
- [ ] T-3.5 Docker read-only client (3 GET endpoints), inspect field allowlist, user up/down summary and admin container detail
- [ ] T-3.6 SSE with role-based variants
- [ ] T-3.7 Admin integration-status endpoint
- [ ] T-3.8 Mount re-check (read-only)

### M4 — Launcher and Home [OWNER A-02, A-09]
- [ ] T-4.1 `catalog/0` schema, loader, admin reload (validated, audited)
- [ ] T-4.2 Route registry; `href` only for authorized routes; otherwise the entry is unavailable
- [ ] T-4.3 `/api/v1/auth/verify` forward-auth, enabled only if B-07 approves a `nexus`-mode route
- [ ] T-4.4 Home UI: launcher grid, widget cards, phone and TV layouts, D-pad focus, home-layout preference

### M5 — Household theme [SPEC F-20]
- [ ] T-5.1 `prism/0` schema validation and built-in default
- [ ] T-5.2 Household GET and PUT (admin!, audited), effective-theme endpoint
- [ ] T-5.3 Theme runtime in the browser (material-color-utilities → CSS variables)

### M6 — Hardening and release readiness
- [ ] T-6.1 The full test plan below, all green in CI
- [ ] T-6.2 Security review against SECURITY.md (self-review checklist plus `/security-review`)
- [ ] T-6.3 Example `config.yaml`, `catalog.yaml` and route registry; deployment notes; example Compose file — **documents only**
- [ ] T-6.4 **[INFRA]** Deploy on knowhere — only after Gate D, with the owner present

**Phase 1 exit check (from the Plan):** family members can sign in and see live status. Measured by: login works for an admin and a non-admin account; the user sees health only; the admin sees detail; stopping any one integration leaves everything else working.

---

## Later phases
### Phase 2 — Collector support
- [ ] T-7.1 Signing keys, JWKS, rotation
- [ ] T-7.2 Service registry, `private_key_jwt` token endpoint, scopes, `jti` replay protection, denylist, enable/disable
- [ ] T-7.3 Delegation tokens from `/auth/verify` for `/store/`
- [ ] T-7.4 `PUT /service/catalog` (Collector publishes; the catalog file is retired)

### Phase 3 — Milano support
- [ ] T-8.1 Token exchange — *D-15*
- [ ] T-8.2 Per-user themes
- [ ] T-8.3 Admin broker and Jellyfin admin key — *D-19 [INFRA: creating a key in Jellyfin]*
- [ ] T-8.4 Jellyfin scheduled tasks, Now Playing (admin)

### Phase 4 — Requests and downloads
- [ ] T-9.1 Jellyseerr — *D-11* · T-9.2 Radarr and Sonarr · T-9.3 qBittorrent · T-9.4 Notifications — *D-10*

### Phase 5 — Desktop and theme packs
- [ ] T-10.1 Desktop mode — *D-06* · T-10.2 Theme packs — *D-05*

### Separately approved work only (not scheduled)
- Docker writes (D-18) · deployment API for Collector (D-16) · privileged host agent (D-17) · Photos and DNS widgets (not phased)

---

## Test plan
| Area | What must be shown |
|---|---|
| Authz | Every route × {anonymous, user, admin, stale admin, service}; unlisted routes denied; startup fails on a missing table entry |
| Visibility (A-07, A-08) | User responses (REST and SSE) never contain admin fields: a schema test on the DTOs, plus a test seeded with admin-only values |
| Login | Fake Jellyfin (`httptest`): success, failure (identical bodies), unreachable Jellyfin (502), Quick Connect states, rate limits |
| Sessions (A-10) | 15-minute access expiry; refresh rotation; reuse after the grace window revokes the family; **10 concurrent refreshes with one token all get the same successor and the family survives**; a retry after a lost response within 20 s succeeds; **the 7-day cap can't be exceeded by any refresh sequence** (property test with a fake clock) |
| Revocation (A-16) | Own, admin and revoke-all (needs re-auth); immediate 401 afterwards; Jellyfin logout called; SSE `session.revoked` |
| Role changes | Admin is not honoured past the admin window without re-auth; re-auth by a demoted user gives the user role; admin revocation of a demoted user takes effect at once |
| Jellyfin tokens (A-14, R-10) | Logout is called right after every login and Quick Connect; no token is persisted anywhere (DB scan); no admin key loaded in Phase 1 |
| Audit (A-15) | Every admin action and auth event writes a row; the hash chain verifies; tampering is detected; an audit write failure aborts the admin action |
| Failure isolation (A-17) | For each integration: down, slow (timeout), oversized and erroring → `stale`/`unavailable`; other widgets and the API unaffected; breaker opens and recovers; Nexus starts with every integration down |
| Docker (A-03) | The client can only produce the 3 GET calls; inspect output is filtered (no env, mounts or labels) |
| Host (A-04) | Parsers tested on fixture `/proc` and `/sys` trees; no writes; internal-hdd excluded everywhere |
| Tailscale (A-11) | Non-Tailscale client IPs rejected; `X-Forwarded-For` ignored from untrusted peers; spoofed `Tailscale-User-*` and `X-Nexus-*` headers ignored unless from the trusted proxy |
| CSRF and web | Missing header or a foreign Origin rejected; CSP and other headers present |
| Secrets | Fuzz all responses, logs and audit rows for the loaded secret values: none found |
| Catalog and routes (A-02, A-09) | Invalid catalog rejected and the old one kept; entries without an authorized route have no `href`; `/auth/verify` refuses unknown routes and non-proxy callers |
| Frontend | Vitest (theme runtime, API client refresh logic); Playwright (login, Home on phone and TV viewports, D-pad focus, admin controls hidden for users) |
| Supply chain | `govulncheck`, `npm audit`, image scan in CI |

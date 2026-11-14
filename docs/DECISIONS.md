# Nexus — Decisions

Revision 4 (14 Nov 2026). M1 approved (A-25); implementation in progress on `feat/m1-foundation`.

| Tag | Meaning |
|---|---|
| **[OWNER]** | Explicitly approved by the owner |
| **[SPEC]** | From the Knowhere Platform Plan or the original brief |
| **[REC]** | Engineering recommendation, **not approved** (§C) |
| **Q-xx / B-xx / D-xx** | Open questions needing an owner decision (§D) |

---

## A. Owner-approved decisions

### Architecture (approved 14 Nov 2026)
| ID | Decision as approved |
|---|---|
| A-01 | Nexus is the central authorization and execution boundary. Collector requests deployment operations through Nexus. |
| A-02 | No client-side direct access to backend services. All access goes through Nexus or an explicitly authorized reverse proxy. |
| A-03 | Phase 1 uses read-only Docker access. Container creation, updates, deletion and other privileged operations are out of scope until separately approved. |
| A-04 | Phase 1 supports read-only system metrics. Privileged host operations need a separately designed and approved agent. |
| A-05 | Collector authenticates with short-lived, signed, Nexus-issued service tokens, with defined audience, expiry, revocation and service identity. |
| A-06 | Collector owns the catalog and update workflow. Nexus owns authorization and execution boundaries. |
| A-07 | General system health widgets are visible to all users. |
| A-08 | Detailed viewing activity and administrative metrics are admin-only. |
| A-09 | The launcher reads from a server-managed application catalog. |
| A-10 | Sessions have a seven-day maximum lifetime with revocable refresh tokens. |
| A-11 | Tailscale network membership is mandatory. Additional identity validation is to be assessed. |
| A-12 | Prefer subdomains for service routing, with paths where appropriate. |
| A-13 | Target repository: `ntsrihari/knowhere-nexus`. |
| A-14 | Normal Jellyfin operations use user-specific authentication. Privileged operations use separately protected administrative credentials. |
| A-15 | Administrative audit logging. |
| A-16 | Session revocation. |
| A-17 | Graceful failure isolation when downstream services are unavailable. |

### Phase 1 plan items (approved 14 Nov 2026)
| ID | Decision as approved |
|---|---|
| A-18 | The revised Phase 1 plan is approved in principle. |
| A-19 (B-02) | Target repository `ntsrihari/knowhere-nexus`. **Keep the current planning branch intact until the repository transfer is verified.** |
| A-20 (B-03) | Phase 1 uses a server-side catalog file in Nexus, with a schema designed for future Collector compatibility → CONTRACTS.md §1. |
| A-21 (B-04) | A provisional JSON theme contract (seed colour, light/dark preference, corner radius, font, density, optional wallpaper), compatible with the future Prism specification → CONTRACTS.md §2. |
| A-22 | Requirements to address: Jellyfin credential retention; refresh rotation under concurrent requests; trusted proxy boundaries and Tailscale identity validation; minimum host access for metrics; CSRF, secure cookie attributes and session revocation; version fields on the catalog and theme contracts. Addressed in R-10, R-14, R-15, R-13, R-16 and R-17. |
| A-23 (B-01) | R-01 to R-13 are **not** collectively approved. Each is decided on its own (§C). |
| A-24 | `ntsrihari/knowhere-homelab-archive` is **intentionally public** and is the **authoritative reference for existing Knowhere infrastructure**: services, Compose configuration, mounts, paths, ports, networking and dependencies. It is documentation, **not a deployment target**. It is read-only for Nexus work: no modifications without explicit approval. Its contents are never assumed to be secret. No credentials or sensitive material are ever committed. Any real credentials found are reported separately. Public visibility is not a finding. (Approved 14 Nov 2026) |
| A-25 | **M1 implementation approved** (M1-01 to M1-15), on a feature branch of `ntsrihari/knowhere-nexus`. Exclusions: no infrastructure changes, no deployment, no Docker writes, no privileged host operations, no authentication, no M2+ features. Implementation decisions within the approved architecture are delegated. (Approved 14 Nov 2026) |

### Specified, still in force
S-01 Go · S-02 SvelteKit static · S-03 Jellyfin sign-in · S-04 admin mapping · S-05 keys only in Nexus · S-06 Tailscale only · S-07 socket proxy · S-09 no format or volume delete · S-10 internal-hdd excluded · S-11 write-heavy guardrail (when start is approved) · S-12 typed confirmation (deferred) · S-13–S-15 themes · S-16 Home and Desktop · S-17 Phase 1 scope.

---

## B. Status of the earlier Phase 1 blockers
| ID | Status |
|---|---|
| B-01 | Replaced by the per-item register in §C (A-23) |
| B-02 | Approved (A-19). **The repo doesn't exist yet**: checked 14 Nov 2026, only `knowhere-homelab-archive` was found |
| B-03 | Approved (A-20) |
| B-04 | Approved (A-21) |
| B-05a–c | Defaults stand until changed (R-08, R-01) |
| B-06 to B-10 | Still open; deploy-time only (§D) |

---

## C. Recommendation register (each needs individual approval)

"Blocks M1" means M1 can't be built without a decision on it.

| ID | Recommendation | Security implications | Alternatives | Blocks M1 |
|---|---|---|---|---|
| P-01 | Go 1.23+ with the **standard-library `net/http` ServeMux** (method patterns), `log/slog`. *Changed from chi:* the standard library now covers routing. | Fewest third-party dependencies, smallest supply-chain surface | chi; echo; gin | **Yes** |
| P-02 | SQLite via pure-Go `modernc.org/sqlite`, WAL mode, DB file 0600 | One local file holds sessions and audit; protected by file permissions and the container user. CGO-free static binary | `mattn/go-sqlite3` (CGO); bbolt; Postgres (more RAM) | **Yes** |
| P-03 | SvelteKit `adapter-static`, embedded with `embed.FS`, SPA fallback | Needs SvelteKit's `kit.csp` hash mode so the inline bootstrap script passes a CSP without `'unsafe-inline'` | Separate static server; SvelteKit SSR (rejected by S-02) | **Yes** |
| P-04 | Server-Sent Events for live widgets | One-way; same auth as REST; long-lived connections need per-user caps | Websockets; polling | No (M3) |
| P-07 | Secrets in a separate file (or Docker secrets); Nexus refuses to start if the file is broader than 0600 or owned by another UID | Keeps secrets out of committed config; a permission check stops accidental exposure | Environment variables (leak through `inspect` and `/proc`); SOPS; Vault (heavy) | **Yes** (the loader is in M1) |
| P-08 | No secret editing in the UI in v1 | Removes a high-value attack path | UI editing behind re-auth | No |
| P-11 | Destructive admin actions need a password re-auth within 10 minutes | Limits a stolen session's damage | Typed confirmation only | No (M2) |
| P-12 | Outbound requests only to base URLs from config; never to client-supplied URLs | Prevents SSRF | — | **Yes** (a design rule for the HTTP client) |
| P-13 | Milano token exchange | — | — | No (Phase 3) |
| P-14 | Theme colour scheme computed in the browser (material-color-utilities) | No server-side parsing beyond schema validation | Compute in Go | No (M5) |
| P-15 | `/api/v1`, JSON error envelope, default-deny route table | A consistent, testable authz surface; errors never leak internals | — | **Yes** |
| R-01 | Sessions: 15-minute access token; single-use rotating refresh token; 7-day absolute cap | Short exposure of the access token; reuse detection catches stolen refresh tokens | Longer access tokens; signed JWT sessions (harder to revoke) | No (M2) |
| R-02 | Revocation: own, admin, revoke-all; immediate inside Nexus | Covered in R-16 | — | No (M2) |
| R-03 | Service tokens: `private_key_jwt`, 5-minute service tokens, 120-second delegation tokens, EdDSA | Short-lived, audience-bound, revocable; no shared secrets | mTLS; shared HMAC secret (weaker) | No (Phase 2) |
| R-04 | Tailscale: ACL + IP-range check mandatory; identity headers recorded only | Superseded in detail by R-15 | tsnet in Nexus | No (M2 code; B-08 at deploy) |
| R-05 | Routing: own domain with a DNS-01 wildcard certificate, otherwise a node per service; Collector at `/store/` | A wildcard certificate keeps hostnames out of CT logs; the host-only cookie avoids cross-subdomain exposure | Paths only on `ts.net` | No (deploy, B-06) |
| R-06 | Authorized route registry, with each route approved individually | Puts A-02 into practice | — | No (M4) |
| R-07 | Catalog file | **Approved as A-20**; contract is R-17 / CONTRACTS.md | — | (schema in M1) |
| R-08 | Widget field visibility table | Admin data on separate endpoints and DTOs | — | No (M3) |
| R-09 | Failure isolation: timeouts, 2 MB caps, bulkheads, circuit breakers, cache-only widgets | Stops a slow upstream from exhausting Nexus | — | No (M3) |
| R-10 | **Revised:** don't keep Jellyfin user tokens (see below) | Removes stored long-lived Jellyfin credentials, including admins' | Keep them encrypted (rev 2) | No (M2) |
| R-11 | Audit: append-only, hash-chained; fail-closed for admin actions; 365-day retention | Evidence of tampering; no silent admin actions | Logs only | No (M2) |
| R-12 | Theme contract | **Approved as A-21**; versioning in R-17 | — | (schema in M1) |
| R-13 | **Revised:** minimum host access (below) | No `/proc` bind, no devices, no capabilities | Bind `/proc`/`/sys`; host exporter | No (M3; deploy B-09) |
| R-14 | **New:** refresh rotation under concurrency (below) | Prevents false logouts without weakening reuse detection | Plain rotation; no rotation | No (M2) |
| R-15 | **New:** trusted proxy boundary and Tailscale identity validation (below) | Spoofing protection for client IPs and identity headers | — | No (M2) |
| R-16 | **New:** CSRF, cookie attributes, revocation (below) | — | — | No (M2) |
| R-17 | **New:** contract versioning (CONTRACTS.md §0) | Strict validation stops malformed or typo'd input; anchor/alias ban stops YAML bombs | Lenient parsing | **Yes** |

### R-10 (revised) — Jellyfin user credentials: don't retain them
- **Finding:**
  - Jellyfin access tokens don't expire until logout or device removal.
  - An **admin's token is a full Jellyfin admin credential**, so keeping it (rev 2) would store admin credentials outside the protected path A-14 requires.
  - Nexus also has **no normal Jellyfin operation that needs a user token after sign-in**. Milano talks to Jellyfin through its own client, and Phase 1 Nexus needs only identity and role.
- **Recommendation:**
  - At login (password or Quick Connect), Nexus authenticates with Jellyfin using a per-login device id, reads the user id, name and `Policy.IsAdministrator`, then **immediately calls `POST /Sessions/Logout`** with that token and throws it away.
  - Nothing Jellyfin-related is stored except user id, name and role.
  - A-14 is still met: the user's own credentials are the only Jellyfin authentication Nexus uses.
- **Consequences:**
  - **Demotion latency.** Without a stored token, Nexus can't re-check the role in Phase 1.
    - Proposal: the admin role is honoured for **12 hours** after the last verification (login or re-auth), then the session continues as a user until the person re-authenticates.
    - An admin can revoke a demoted person's sessions at once (R-16).
    - From Phase 3, the admin broker can re-check roles server-side.
  - **Logout.** There's no Jellyfin token left to log out. Revoking a Nexus session affects only Nexus.
- **To check during M2:** that `/Sessions/Logout` invalidates only that token on the deployed Jellyfin version.
- **Alternative:** keep tokens encrypted (rev 2) for a 5-minute role re-check. Faster demotion, but stored admin-equivalent credentials.
- **Decision needed: Q-01** (approach) and **Q-02** (the 12-hour window).

### R-13 (revised) — Minimum host access for Phase 1 metrics
| Metric | Source | Access needed |
|---|---|---|
| CPU %, memory, load, uptime | `/proc/stat`, `/proc/meminfo`, `/proc/loadavg`, `/proc/uptime` | **None extra.** A container's own `/proc` reports these host-wide (no lxcfs on knowhere is assumed — check at deploy) |
| Temperature | `/sys/class/hwmon/*/temp*_input`, `/sys/class/thermal/*` | **None extra.** Docker mounts the host's sysfs read-only (check at deploy; no lxcfs or sysfs masking expected) |
| Mount present, ro/rw, free space | `/proc/self/mountinfo` + `statfs()` | **A read-only bind of each configured mount point, with `rslave` propagation** (for example `/mnt/media:/host/mnt/media:ro,rslave`), so host mount and unmount events show up inside the container |
| `/mnt/internal-hdd` | — | **Not mounted at all.** Reported as `excluded` from config only (S-10) |
| SMART, devices | — | **None.** Deferred (A-04, D-17) |

- **Explicitly not granted:**
  - a host `/proc` bind (it would expose every host process's command line, which can contain secrets);
  - `/dev`, capabilities, privileged mode, the host PID or network namespace;
  - the Docker socket (that's the proxy's job).
- **Residual risk:** the read-only binds let the Nexus process *read* media and photos.
  - Mitigations: non-root UID; Nexus code only calls `statfs` and never opens files there (enforced by test).
  - **Alternative without data exposure:** a host-side read-only exporter (a systemd timer writing `findmnt -J` and `df` output to `/run/knowhere/storage.json`, bind-mounted read-only). That's host software, so infrastructure needing approval.
- **Decision needed: Q-03.**

### R-14 — Refresh rotation under concurrent requests
- **Problem:** several tabs, parallel 401 retries, or a response lost after the server rotated can all present the same refresh token twice. Plain reuse detection would revoke the family and log the user out by mistake.
- **Design:**
  1. **Atomic rotation.** Inside a `BEGIN IMMEDIATE` transaction: `UPDATE refresh_tokens SET used_at = now, successor_id = ? WHERE hash = ? AND used_at IS NULL AND family not revoked`. Only one caller wins.
  2. **Grace window (20 s).** A refresh token used within the last 20 s returns **the same successor pair**. To allow that, the successor's plaintext is kept encrypted (AES-GCM, key from secrets) only for the window, then wiped. No new tokens are minted for repeats.
  3. **Reuse after the window** → the family is revoked, a security audit event is written, and the response is 401.
  4. **Client single-flight.** The SPA uses the Web Locks API (`navigator.locks`) so only one tab refreshes at a time; the others wait and then retry with the new cookie. Milano does the same with an in-process mutex.
  5. The successor inherits the family's absolute expiry. Old access tokens just expire (≤ 15 minutes); they aren't revoked at rotation, which would cause the same race.
- **Security cost:** for 20 s a stolen refresh token gives the same pair as the legitimate one. It doesn't give an extra session, and use after the window is still detected.
- **Decision needed: Q-04** (the 20 s window).

### R-15 — Trusted proxy boundary and Tailscale identity validation
**Chain** (final shape depends on B-06): `client → tailnet ACL → TLS front end (Tailscale Serve, or Caddy on the tailnet IP) → Caddy → Nexus`.

| Boundary | Rule |
|---|---|
| Nexus listener | Binds only to its address on the private `edge` Docker network. No host-published port. |
| Trusted peer | `trustedProxies` in config is an exact list of Caddy's address(es) (/32 or /128). Connections from any other peer are refused with 403, **except** `/healthz` from loopback (container healthcheck). |
| Client IP | Taken from `X-Forwarded-For` **only** when the peer is trusted, using the right-most entry not in `trustedProxies`. Caddy must set or override XFF and must trust only the TLS front end's address. |
| Tailnet membership | The client IP must be in `100.64.0.0/10` or `fd7a:115c:a1e0::/48`. Otherwise 403 and an audit entry. Together with the tailnet ACL (infrastructure, B-08), this implements A-11. |
| `Tailscale-User-Login` / `-Name` | Read only from a trusted peer, and **only if the TLS front end is Tailscale Serve**. Phase 1 records them in the session and audit log **only**. They're never used for authorization until it's confirmed that Serve overwrites any client-supplied copies on the deployed version. Absent for tagged or shared-in devices: allowed, recorded as null. |
| `X-Nexus-*` headers | Caddy deletes all inbound copies. Nexus ignores them unless the peer is trusted. |
| `/api/v1/auth/verify` | Answers only trusted peers. |

**Tailscale identity options (assessment for A-11):**
- Recommended: (1) ACL, (2) IP range, (3) record headers.
- Session binding to the Tailscale login: **off**, pending confirmation of header stripping (Q-07).
- Alternatives:
  - the `caddy-tailscale` plugin (Caddy does its own WhoIs);
  - `tsnet` inside Nexus.
- Rejected: mounting the LocalAPI socket into Nexus.

### R-16 — CSRF, cookie attributes and session revocation

**Cookies:**

| Cookie | Attributes |
|---|---|
| `__Host-nexus_access` | `Secure; HttpOnly; SameSite=Strict; Path=/`; no `Domain`; `Max-Age=900` |
| `__Secure-nexus_refresh` | `Secure; HttpOnly; SameSite=Strict; Path=/api/v1/auth`; no `Domain`; `Max-Age` = seconds left until the absolute expiry |

- The `__Host-` prefix stops a sibling subdomain from setting or overwriting the access cookie (cookie tossing).
- No cookie is readable from JavaScript.

**CSRF:** all of these apply to every unsafe method (POST, PUT, PATCH, DELETE) authenticated by cookie, **including login**:
1. `Origin` must equal the configured public origin exactly. If `Origin` is missing, `Sec-Fetch-Site: same-origin` is required; otherwise the request is rejected.
2. A custom header `X-Nexus-CSRF: 1` is required. Cross-origin JavaScript can't send it without a CORS preflight, and **Nexus never sends CORS headers**.
3. SameSite=Strict. Note that sibling subdomains count as *same-site*, which is why checks 1 and 2 are mandatory.

Further rules:
- GET and HEAD handlers must not change state (enforced by test).
- When an `Authorization: Bearer` header is present, cookies are ignored.

**Revocation:**
- **Triggers:** logout; a user revoking their own session; an admin revoking a session or all of a user's sessions; refresh reuse; absolute expiry; (later) a Tailscale-binding mismatch.
- **Effect:**
  - In one transaction: the family is marked revoked, and its access and refresh rows are deleted.
  - The in-memory credential cache is evicted synchronously, since Nexus is a single process.
  - Open SSE streams for the family get `session.revoked` and are closed within 1 s.
  - Phase 2 delegation tokens: at most 120 s of staleness; every execution path re-checks `sid`.
- **Response:** revoked credentials get 401 `unauthenticated`. The client deletes its cookies with `Max-Age=0` on logout.

---

## D. Open decisions needing the owner

### For M1 (approve these, or amend them, to start M1)
| ID | Decision |
|---|---|
| M1-a | P-01 (standard-library HTTP), P-02 (modernc SQLite), P-03 (SvelteKit static + embed + CSP hashes), P-07 (secrets file), P-12 (no client URLs), P-15 (API conventions), R-17 (contract versioning) |
| M1-b | Create `ntsrihari/knowhere-nexus` and attach it to this environment (A-19) |
| M1-c | Explicit approval to start M1 |

### For M2 (before authentication work)
| ID | Decision |
|---|---|
| Q-01 | R-10: don't keep Jellyfin tokens (recommended), or keep them encrypted |
| Q-02 | Admin-role window without a re-check: 12 hours? |
| Q-04 | R-14: 20 s refresh grace window |
| Q-07 | R-15: Tailscale-login binding off until header stripping is verified |
| — | R-01, R-02, R-11, R-15, R-16, P-11 |

### For M3 to M5
| ID | Decision |
|---|---|
| Q-03 | R-13: read-only `rslave` binds of the data mounts (data readable by Nexus), or a host exporter (infrastructure) |
| Q-05 | Theme `colorScheme`: add `system`? It wasn't in the approved field list |
| Q-06 | Bundled font allowlist (proposal: Inter, Roboto, Roboto Flex, JetBrains Mono; self-hosted, open licences) |
| — | R-06, R-08, R-09, P-04, P-14 |

### Deploy-time (infrastructure)
B-06 routing and TLS · B-07 authorized routes · B-08 tailnet ACL · B-09 host mounts (now Q-03) · B-10 deploy the socket proxy and Nexus.

### Found during review
Q-08 (archive visibility) — **closed by A-24**: the visibility is intentional.

### Later (unchanged)
D-03 write-heavy mapping · D-04 start action · D-05 theme packs · D-06 wallpaper uploads · D-10 notifications · D-11 Jellyseerr identity · D-15 Milano and the 7-day cap · D-16 deploy API · D-17 host agent · D-18 Docker writes · D-19 Jellyfin admin key · D-20 shared location for contracts · D-21 retire or replace the existing `dashboard` stack (ARCHITECTURE §9) · D-22 reuse the existing `dashboard-socket-proxy` or run a dedicated Nexus proxy (recommendation: dedicated, on an internal network, no host port).

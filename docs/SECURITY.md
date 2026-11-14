# Nexus — Security Design

Revision 3 (14 Nov 2026). Tags: **[OWNER]**, **[SPEC]**, **[REC]** (not approved; see the DECISIONS.md register). The detailed designs live in DECISIONS.md (R-10, R-13 to R-17). This file is the security view and the test obligations.

Phase 1 keeps Nexus **read-only towards Docker and the host [OWNER A-03, A-04]** and, if R-10 is approved, **holds no Jellyfin credentials**. So the worst case in Phase 1 is information disclosure, not control.

## 1. Assets (Phase 1)
| Asset | Exposure |
|---|---|
| Docker API | Read-only proxy. `inspect` is filtered: no environment variables, mounts or labels leave Nexus |
| Host | Container's own `/proc` and `/sys` (host-wide counters); read-only `rslave` binds of data mounts if Q-03 allows |
| Downstream API keys | In memory, from the secrets file. Few used in Phase 1 |
| Jellyfin credentials | **None stored** (R-10). Passwords pass through memory once, at login |
| Nexus access and refresh tokens | SHA-256 hashes at rest; successor plaintext encrypted for 20 s only (R-14) |
| Audit log | Hash-chained |
| Secrets file | Encryption key for R-14, and later the signing keys |

## 2. Threats and controls
| Actor | Threat | Controls |
|---|---|---|
| Off-tailnet or non-family device | Reaching Nexus | Tailnet ACL **[INFRA B-08]**; trusted-peer and Tailscale-range checks (R-15) |
| Non-admin family member | Admin data or actions | Default-deny route table; separate admin endpoints and DTOs (A-08) |
| Demoted admin | Residual admin access | Admin role honoured for 12 h after verification (Q-02); immediate revocation by another admin (R-16); server-side re-checks from Phase 3 |
| Cookie or refresh-token theft | Session hijack | 15-minute access; rotation with reuse detection (R-14); 7-day cap; revocation |
| Malicious page, or a compromised sibling subdomain | CSRF, cookie tossing | Origin plus custom header, no CORS, `__Host-` cookie, SameSite=Strict (R-16) |
| Spoofed proxy headers | Fake client IP or Tailscale identity | Headers trusted only from `trustedProxies`; identity used for audit only (R-15) |
| Compromised downstream service | XSS, resource exhaustion | Output escaping, size caps, timeouts, breakers (R-09) |
| Malicious catalog or theme input | Parser abuse, injection | Strict schemas, size caps, no YAML aliases, built-in icons only (R-17, CONTRACTS.md) |
| Supply chain | Malicious dependency | Standard-library HTTP (P-01), pinned dependencies, `govulncheck`, `npm audit`, gitleaks, distroless image, CI-only builds |

## 3. Security-sensitive operations (Phase 1)
| Operation | Role | Controls |
|---|---|---|
| Login, Quick Connect | public | CSRF checks (login included); rate limits (5 failures per username and 20 per IP per 15 minutes); one failure message; immediate Jellyfin token logout (R-10); audit |
| Refresh | refresh cookie | Atomic rotation, 20 s grace, reuse → family revoked (R-14) |
| Re-auth | user | Jellyfin password check; resets admin verification |
| Revoke own session | user | Ownership check; audit |
| Revoke another session | admin | Audit |
| Revoke all of a user's sessions | admin + re-auth | Audit |
| Household theme | admin | Theme contract validation; audit |
| Catalog reload | admin | Catalog contract validation, revision check; audit |
| Mount re-check | admin | Read-only; audit |
| Forward-auth verify | trusted peer only | Approved route registry |
| Out of scope | — | Docker writes, deploys, host power, SMART (A-03, A-04) |

## 4. Controls
### 4.1 Network boundary [OWNER A-02, A-11; REC R-15]
- The listener is on the private `edge` network only.
- `trustedProxies` is an exact list. Any other peer is refused, except loopback `/healthz`.
- Client IP comes from `X-Forwarded-For` only through a trusted peer (right-most untrusted entry), and must be in a Tailscale range.
- Tailscale identity headers: trusted peer and Serve front end only; recorded, never used for authorization (Q-07).
- No CORS headers, ever.

### 4.2 Docker [OWNER A-03]
- Proxy profile `CONTAINERS=1, POST=0`, everything else 0, on an internal network.
- The client has only three GET calls. `inspect` passes through a field allowlist.

### 4.3 Host [OWNER A-04; REC R-13]
- No `/proc` bind, no `/dev`, no capabilities, no privileged mode, no host namespaces.
- Data-mount binds are read-only with `rslave`. Nexus only calls `statfs` (enforced by test). `/mnt/internal-hdd` is never mounted.

### 4.4 Sessions, cookies, CSRF, revocation [OWNER A-10, A-16; REC R-01, R-14, R-16]
- Cookies: `__Host-nexus_access` (Path=/, 900 s) and `__Secure-nexus_refresh` (Path=/api/v1/auth). Both `Secure; HttpOnly; SameSite=Strict`, no `Domain`.
- CSRF: an exact Origin match (or `Sec-Fetch-Site: same-origin`), plus `X-Nexus-CSRF: 1`, on every cookie-authenticated unsafe method, login included. GET and HEAD never change state.
- Bearer requests ignore cookies.
- Refresh: atomic compare-and-set, 20 s idempotent grace, reuse after it → revoke, client single-flight.
- Revocation: transactional; cache evicted synchronously; SSE closed within 1 s; 401 afterwards.

### 4.5 Jellyfin [OWNER A-14; REC R-10]
- A per-login device id. The token is used once to read identity and role, then logged out and dropped.
- No Jellyfin admin key in Phase 1.

### 4.6 Secrets [SPEC S-05; REC P-07, P-08]
- A secrets file, refused if broader than 0600 or not owned by the Nexus UID.
- A redacting type (`String`, `MarshalJSON`, `slog.LogValuer`).
- Never in config, the image, the repo, logs, responses or audit.

### 4.7 Web [REC]
- CSP without `'unsafe-inline'`: `default-src 'self'; script-src 'self' 'sha256-<index.html inline bootstrap>'; style-src 'self'; style-src-attr 'unsafe-hashes' 'sha256-<SvelteKit route-announcer style>'; img-src 'self' data:; font-src 'self'; connect-src 'self'; frame-src 'none'; frame-ancestors 'none'; object-src 'none'; base-uri 'none'; form-action 'self'`.
  - Script hashes are computed by Go at startup from the embedded `index.html`.
  - `style-src-attr` allows only the exact, fixed style attribute of SvelteKit's accessibility live region (a constant in `internal/app`). Any other inline style is blocked. `make e2e` fails if a SvelteKit upgrade changes it.
- Also: `nosniff`, `Referrer-Policy: no-referrer`, `Cross-Origin-Opener-Policy: same-origin`, `Permissions-Policy` that disables unused features.
- HTTP server limits: `ReadHeaderTimeout` 5 s, `ReadTimeout` 15 s, `IdleTimeout` 60 s, 16 KB of headers, 1 MB request bodies (catalog reload reads the file, not the body).

### 4.8 Audit [OWNER A-15; REC R-11]
- Append-only, hash-chained, with a verification endpoint.
- If the audit write fails, the admin action fails.

### 4.9 Contracts [OWNER A-20, A-21; REC R-17]
- Strict schemas with `schema` and `schemaVersion`.
- Size caps (catalog 256 KB, theme 4 KB). YAML anchors, aliases and duplicate keys rejected.
- Built-in icons and wallpapers only in Phase 1. Fonts from a bundled allowlist only.

### 4.10 Permanent guardrails [SPEC S-09, S-10]
- No format, delete-volume or remove-container code paths, asserted by tests.
- `/mnt/internal-hdd` is always excluded.

## 5. Security test gates
- **Before M2 starts:** the M1 security tests in TASKS.md §M1.
- **Before Phase 1 deploy:** the full matrix in TASKS.md "Test plan", plus a `/security-review` pass.

## 6. Reporting
Report security issues privately to the repository owner.

# Nexus — Architecture

Revision 3 (14 Nov 2026). **Not implemented.**

> **Revision 3 changes.** These override any older text below that conflicts with them.
> - **Jellyfin tokens are no longer kept** (R-10 revised): the token is logged out at login, and the admin role is honoured for 12 hours after verification.
> - The `jellyfin_token_enc` column is removed.
> - Host access is minimal: no `/proc` or `/sys` binds, only read-only `rslave` data-mount binds (R-13 revised).
> - New designs: refresh concurrency (R-14), the trusted proxy boundary (R-15), and CSRF, cookies and revocation (R-16).
> - Catalog and theme contracts are defined in CONTRACTS.md, both versioned (R-17).
> - HTTP routing uses the standard library (P-01 revised).
> - Recommendations need **individual** approval (A-23). Tags: **[OWNER]** owner-approved · **[SPEC]** Platform Plan or brief · **[REC]** recommendation pending approval · **[BLOCKER]**/**[LATER]** open. Decision ids refer to DECISIONS.md.

## 1. Role of Nexus
Nexus is the dashboard and API at `knowhere/` **[SPEC]**, and **the central authorization and execution boundary** of the platform **[OWNER A-01]**. Concretely:
- It is the only component that holds downstream service credentials **[SPEC S-05]**.
- It is the only component that reaches Docker (read-only in Phase 1) and the host (read-only metrics in Phase 1) **[OWNER A-01, A-03, A-04]**.
- Clients reach services only through Nexus or an explicitly authorized reverse-proxy route **[OWNER A-02]**.
- Collector owns the catalog and update *workflow*. Any execution it needs is requested from Nexus, which authorizes and executes it **[OWNER A-01, A-06]**. That request API isn't designed yet and is out of scope **[OWNER A-03; LATER D-16]**.

Out of scope: Collector, Milano and Prism code; Caddy, Tailscale and socket-proxy configuration (documented here as requirements, not built); any change to knowhere.

## 2. Requirements

### 2.1 Functional
| ID | Requirement | Phase | Source |
|---|---|---|---|
| F-01 | Sign in with a Jellyfin password | 1 | SPEC |
| F-02 | Sign in with Jellyfin Quick Connect | 1 | SPEC |
| F-03 | Jellyfin admin → Nexus admin | 1 | SPEC |
| F-04 | Home mode: launcher grid plus widgets; works on phones and TVs | 1 | SPEC |
| F-05 | System health widget (CPU, RAM, load, temperature, uptime), visible to all users | 1 | SPEC, OWNER A-07 |
| F-06 | Storage widget: summary for all users, detail for admins. SMART deferred. | 1 | SPEC, OWNER A-04/A-07/A-08 |
| F-07 | Services widget: up/down for all users; container detail for admins | 1 | SPEC, OWNER A-07/A-08 |
| F-08 | Now Playing, admin-only | 3 | SPEC, OWNER A-08 |
| F-09 | Photos widget (Immich) | not phased | SPEC |
| F-10 | DNS widget (Pi-hole) | not phased | SPEC |
| F-11 | Downloads widget (qBittorrent) | 4 | SPEC |
| F-12 | Upcoming widget (Radarr, Sonarr) | 4 | SPEC |
| F-13 | Container restart, stop and update | **Out of scope until separately approved** | SPEC, then OWNER A-03 |
| F-14 | Run a Jellyfin scheduled task (admin credential) | 3 | SPEC, OWNER A-14 |
| F-15 | Re-check mounts, as a read-only refresh | 1 | SPEC, OWNER A-04 |
| F-16 | Host restart and shutdown | **Out of scope until the agent is approved** | SPEC, then OWNER A-04 |
| F-17 | Write-heavy mount guardrail on start | When start is approved | SPEC |
| F-18 | `/mnt/internal-hdd` never shown as usable | 1 | SPEC |
| F-19 | No format or delete-volume action | always | SPEC |
| F-20 | Household default theme | 1 | SPEC |
| F-21 | Per-user themes | 3 | SPEC |
| F-22 | Theme packs | 5 | SPEC |
| F-23 | Desktop mode | 5 | SPEC |
| F-24 | Milano server control through Nexus (read-only parts are available once the Phase 1 endpoints exist) | 3 | SPEC |
| F-25 | Milano token exchange | 3 | SPEC |
| F-26 | Collector authenticates with signed, short-lived Nexus service tokens | 2 | OWNER A-05 |
| F-27 | Requests and \*arr through Nexus | 4 | SPEC |
| F-28 | Launcher reads a server-managed catalog | 1 | OWNER A-09 |
| F-29 | Seven-day maximum sessions with revocable refresh tokens | 1 | OWNER A-10 |
| F-30 | Session revocation (own sessions; admins for anyone) | 1 | OWNER A-16 |
| F-31 | Administrative audit log | 1 | OWNER A-15 |
| F-32 | Graceful, isolated failure when downstream services are down | 1 | OWNER A-17 |

### 2.2 Non-functional
| ID | Requirement | Source |
|---|---|---|
| NF-01 | Go, single binary, about 30–60 MB RAM | SPEC |
| NF-02 | SvelteKit compiled to static files | SPEC |
| NF-03 | Tailscale membership mandatory | SPEC, OWNER A-11 |
| NF-04 | Docker through a restricted proxy; read-only in Phase 1 | SPEC, OWNER A-03 |
| NF-05 | No API keys in apps or repos | SPEC |
| NF-06 | Built only in GitHub Actions; image in GHCR | SPEC |
| NF-07 | Subdomain routing preferred; paths where appropriate | OWNER A-12 |
| NF-08 | Code lives in `ntsrihari/knowhere-nexus` | OWNER A-13 |

## 3. System context

```
 Tailnet devices ─▶ [ACL: family group → knowhere:443]                     (REC R-04, INFRA)
                     │
                     ▼
            Tailscale Serve / TLS ─▶ Caddy  (authorized route registry, REC R-06)
                     │                 ├─ nexus host  /           ─▶ Nexus
                     │                 ├─ nexus host  /store/     ─▶ forward_auth Nexus ─▶ Collector   (REC R-05)
                     │                 └─ approved service routes ─▶ upstream (auth mode per route)   (BLOCKER B-07)
                     ▼
                   Nexus ──login only──▶ Jellyfin          (OWNER A-14, R-10)
                     ├────admin broker───▶ Jellyfin admin key (Phase 3, LATER D-19)
                     ├──▶ Immich, Pi-hole, *arr, qBittorrent (keys in secrets)
                     ├──▶ docker-socket-proxy [read-only profile] ─▶ docker.sock   (OWNER A-03)
                     └──▶ host /proc, /sys (read-only bind)                     (OWNER A-04, BLOCKER B-09)
 Collector ──service token + delegation token──▶ Nexus                        (OWNER A-05, REC R-03)
```

Trust boundaries:
1. Tailnet to TLS front end.
2. Caddy to Nexus.
3. Nexus to downstream services.
4. Nexus to the Docker proxy.
5. Nexus to the host.
6. Collector to Nexus.

## 4. Backend [REC unless marked]

### 4.1 Packages
```
cmd/nexus/                main, wiring
internal/config/          config.yaml, secrets file, catalog.yaml, route registry, service registry
internal/httpapi/         stdlib ServeMux, middleware, handlers (P-01)
internal/auth/            login, Quick Connect, sessions (access + refresh families), revocation, verify (forward-auth)
internal/tokens/          Ed25519 signing keys, JWKS, service tokens, delegation tokens, jti denylist   (Phase 2)
internal/authz/           default-deny route→role table; startup check
internal/jellyfin/        login-only client (authenticate, read role, logout token) (R-10); admin broker (Phase 3)
internal/integrations/    immich, pihole, qbittorrent, radarr, sonarr, prowlarr, jellyseerr
internal/resilience/      timeouts, size caps, bulkheads, circuit breakers               (OWNER A-17)
internal/docker/          read-only client for the socket proxy (list, inspect, stats)  (OWNER A-03)
internal/host/            container /proc and /sys readers, mountinfo + statfs only     (OWNER A-04, R-13)
internal/storage/         mount model, internal-hdd exclusion, summary/detail split
internal/widgets/         pollers, cache, visibility filter (R-08), SSE publishing
internal/catalog/         knowhere.catalog contract: file in Phase 1, Collector from Phase 2 (CONTRACTS.md)
internal/theme/           knowhere.theme contract validation, precedence (CONTRACTS.md)
internal/audit/           append-only hash-chained audit writer                        (OWNER A-15)
internal/store/           SQLite and migrations
web/                      SvelteKit (adapter-static), embedded
```

### 4.2 Request pipeline
`recover → request id → security headers → trusted-proxy and Tailscale-range check (R-04 option 2) → record Tailscale identity headers (R-04 option 3) → authenticate (access cookie or bearer) → authz table (default deny) → rate limit → fresh-admin check where required → handler → audit`

### 4.3 Sessions [OWNER A-10, A-16; REC R-01, R-02]
- A family is created at login, with an absolute expiry of login + 7 days that is never extended.
- The access credential lives 15 minutes. The refresh credential is single-use and rotating.
- Reuse of a refresh credential revokes the family.
- Each family also stores the last role verification time and the Tailscale login (when present). It stores no Jellyfin token (R-10).
- Refresh rotation under concurrency: R-14. Cookies, CSRF and revocation: R-16.
- Revocation deletes the family's credentials and calls Jellyfin logout.

### 4.4 Jellyfin [OWNER A-14; REC R-10]
- Phase 1: the user's own credentials are used once at login to read identity and role. The resulting token is logged out at once and never stored (R-10).
- The admin broker, holding the dedicated admin key, arrives in Phase 3, behind a fresh-admin check and audit.

### 4.5 Docker [OWNER A-03]
- The client implements only `GET /containers/json`, `GET /containers/{id}/json` and `GET /containers/{id}/stats?stream=false`.
- There is no code path for any write.
- The proxy runs a read-only profile (`CONTAINERS=1`, `POST=0`; everything else 0).

### 4.6 Host [OWNER A-04; REC R-13]
- Reads the container's own `/proc/{stat,meminfo,loadavg,uptime}` and `/sys/class/{hwmon,thermal}` (host-wide values; check at deploy).
- Mount status and free space come from `/proc/self/mountinfo` and `statfs` on read-only `rslave` binds of each configured mount (R-13, Q-03).
- No privileges, no host PID namespace, no `/dev`.

### 4.7 Failure isolation [OWNER A-17; REC R-09]
- Every integration call goes through `resilience`.
- Widgets read only from cache.
- Nexus starts with every integration down.
- Jellyfin outage: existing sessions and refresh keep working (checked locally); new logins and re-auth fail with `upstream_unavailable`. The admin role follows the 12-hour verification window (R-10, Q-02).

### 4.8 Catalog and launcher [OWNER A-09; REC R-07]
- Phase 1: `catalog.yaml` in the versioned `knowhere.catalog` 0.1.0 contract (CONTRACTS.md §1), read-only and reloadable by an admin (audited).
- Only `kind: server` entries are shown in Phase 1.
- An entry whose `route` isn't in the authorized registry is shown as unavailable **[OWNER A-02]**.

### 4.9 Audit [OWNER A-15; REC R-11]
- Append-only, hash-chained table.
- If the audit write fails, the admin action fails.

## 5. Frontend [REC]
- SvelteKit `adapter-static` SPA. Routes: `/login`, `/` (Home), `/settings` (theme, sessions), `/admin` (audit, sessions, catalog reload, integration health), `/desktop` (Phase 5).
- The API client handles a 401 by refreshing once, then retrying. If the refresh fails, it goes to `/login`.
- Admin views show only when the role says admin; the server enforces it regardless.
- Theme runtime: `knowhere.theme` JSON to CSS variables with material-color-utilities (P-14).
- TV and phone: responsive grid, D-pad focus order, no hover-only controls.
- Clients never see service URLs, only route URLs from the authorized registry.

## 6. Data model [REC]
| Table | Fields |
|---|---|
| `session_families` | id, jellyfin_user_id, username, role, role_verified_at, reauth_at, tailscale_login, device_label, kind (browser or milano), created_at, absolute_expires_at, revoked_at, revoked_by, revoke_reason |
| `access_tokens` | hash, family_id, expires_at |
| `refresh_tokens` | hash, family_id, expires_at, used_at, successor_id, successor_enc (kept 20 s only, R-14) |
| `user_prefs` | jellyfin_user_id, theme_json, home_layout_json, desktop_layout_json, updated_at |
| `household` | key, value_json, updated_by, updated_at |
| `audit` | seq, at, prev_hash, hash, actor_user_id, actor_name, tailscale_login, action, target, params_json, result, request_id |
| `jti_denylist` (Phase 2) | jti, expires_at |

## 7. Dependencies
| Component | Nexus needs | It needs from Nexus | Status |
|---|---|---|---|
| Prism | Theme schema | Theme storage and serving | Provisional `knowhere.theme` 0.1.0, approved (A-21) |
| Collector | Catalog publishing (Phase 2); later, deployment requests | Service tokens, delegation tokens, JWKS, `/store/` forward-auth, catalog publish API | Phase 2; deployment API LATER D-16 |
| Milano | — | Token exchange, read-only health endpoints, per-user theme | Phase 3; D-15 |
| Infra | Routing/TLS (B-06), authorized routes (B-07), tailnet ACL (B-08), host mounts (B-09), socket proxy (B-10) | — | Owner approval for each |

## 8. Deployment [REC; INFRA approval required to apply]
- Image `ghcr.io/ntsrihari/knowhere-nexus:<version>`: distroless, non-root, read-only root filesystem, no added capabilities.
- Volumes: `config/` (read-only), `secrets/` (read-only, 0600), `data/` (SQLite), read-only `rslave` binds of configured data mounts (Q-03). No `/proc`, `/sys` or `/dev` binds.
- Networks: `nexus-internal` (Nexus and the socket proxy only), `edge` (Caddy and Nexus), plus each integration's network as needed.
- The Compose file goes to `knowhere-homelab-archive` only after owner approval (B-10).

## 9. Existing Knowhere infrastructure (reference: `knowhere-homelab-archive` @ `d4166a5`)
Source of truth: the archive repository (A-24). This section only records what affects Nexus; re-check the archive before any deployment work.

| Item | Current state (from the archive) | Effect on Nexus |
|---|---|---|
| Existing dashboard | `dashboard/` stack: a Go dashboard on **host port 80** (`network_mode: host`, `pid: host`, `SYS_PTRACE`, AppArmor unconfined) plus Caddy | Nexus is meant to replace it at `knowhere/` (Plan). Cut-over and retirement are an infrastructure decision (D-21). Nexus deliberately avoids host networking and host PID |
| Docker socket proxy | `dashboard-socket-proxy` (`tecnativa/docker-socket-proxy:latest`, `CONTAINERS=1`, `POST=0`) published on `127.0.0.1:2375` | Matches the read-only profile Nexus needs. Reuse or a dedicated proxy: D-22 |
| Published service ports | Jellyfin 8096, Navidrome 4533, Immich 2283, Portainer 9443, Pi-hole 8081 (on LAN and Tailscale IPs), Black Box 8787, qBittorrent 8080 (prepared on a branch, not deployed), tv-stream 8765 (Tailscale IP); Home Assistant 8123 and MeshCentral 4430 use host networking | Nexus listens inside its container on **8480** and publishes no host port, so there's no conflict |
| Tailscale | Address observed as `100.104.30.99`; some services bind to it directly | Matches the Tailscale-range rule (R-15) |
| Storage | `/mnt/media` (Kingston, NTFS via ntfs-3g, **read-only**, UUID `D09481079480F0F0`); `/mnt/immich` (One Touch, exFAT, read-write, UUID `AD95-6C91`); Seagate and `/mnt/internal-hdd` on hold, never used | Configured mount list for the storage widget (M3); internal-hdd excluded (S-10) |
| Startup guards | systemd units check UUIDs and paths before `docker compose up` | Nexus deployment should follow the same pattern (B-10) |
| Not present | Nextcloud Compose is not in the archive | Nexus mustn't assume Nextcloud exists |
| Credentials | The archive keeps secrets in git-ignored `.env` files; a scan at `d4166a5` found only generators and placeholders | — |

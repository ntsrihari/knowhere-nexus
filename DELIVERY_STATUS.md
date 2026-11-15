# Knowhere — delivery status

Updated 15 Nov 2026. Branches: `feat/m1-foundation` (M1, complete), `feat/m2-auth` (M2–M6 and integrations).

## Nexus (this repo): working
| Area | State | Verified by |
|---|---|---|
| Sign-in (password, Quick Connect), sessions, revocation, roles | Done | `internal/session`, `internal/app/auth_test.go`, browser e2e |
| Audit log (hash chain, verify) | Done | `internal/audit`, e2e "Verify chain" |
| Network boundary (trusted proxy, tailnet), CSRF, cookies | Done | `internal/netguard`, `TestCSRF`, `TestCookieAttributes` |
| Widgets: system, storage, services; live SSE; failure isolation | Done | `features_test.go`, e2e |
| Launcher, catalog reload, Collector catalog publish | Done | `TestLauncherAppsAndCatalogReload`, `tokens_test.go` |
| Themes (household, per user, Material 3 runtime) | Done | `TestThemePrecedenceAndLayout`, `theme.test.ts`, e2e |
| Integrations: Jellyfin admin, Jellyseerr, Radarr, Sonarr, Prowlarr, qBittorrent, Immich, Pi-hole | Done (against fakes) | `integrations_test.go` |
| Notifications, watch analytics, desktop mode, phone/TV layouts | Done | tests + e2e |
| Container (non-root, read-only, 7 MiB idle) | Done | `scripts/container-test.sh` |

Not verified against the real services on Knowhere: everything above runs against fakes that implement each service's documented API. The first real run is part of deployment (needs B-06 to B-10).

## Run it
```bash
make frontend-deps frontend build     # bin/nexus with the UI embedded
make check                            # all Go and frontend tests
make e2e                              # browser flow against the dev fake Jellyfin
make image container-test             # hardened container
```
Configuration: `examples/config.yaml` (every option), `examples/secrets.example.yaml`.

## Other products (built and tested locally; not yet on GitHub)
Their GitHub repositories don't exist, so they live only in this session's container:

| Product | Local path | Commits | Verified |
|---|---|---|---|
| Prism | `/home/user/knowhere-prism` | 5 | 171 web (Vitest) and 74 Kotlin (JUnit) tests pass; TS/Kotlin colour parity golden tests; theme packs; CSP-safe preview |
| Collector | `/home/user/knowhere-collector` | 14 | `go test -race` 24 packages; staticcheck clean; end-to-end flow against fakes; pure-Go APK v2/v3 verification and F-Droid index signing; container test passes (26 MB, 7 MiB idle); deploy execution disabled |
| Milano | `/home/user/knowhere-milano` | 8 | 32 unit tests; lint; debug APKs for phone and Android TV built (`dist/`); sends the Nexus CSRF header (fixed during integration review) |

**Owner action needed:** create empty `ntsrihari/knowhere-prism`, `ntsrihari/knowhere-collector` and `ntsrihari/knowhere-milano`. Each local `main` can then be pushed as-is. Until then this container is the only copy.

Known gaps:
- **Milano:** no device or emulator run yet; music, Live TV, downloads and casting are hidden, not built.
- **Collector:** template image tags aren't digest-pinned; publishing was tested against a fake Nexus only.
- **Prism:** the Gradle wrapper checksum isn't set.

## Blockers and decisions still open
- Deployment to Knowhere: B-06 routing/TLS, B-07 approved routes, B-08 tailnet ACL, B-10 deploy approval.
- Not implemented by decision: Docker writes, host power and SMART, Collector deploy execution (A-03, A-04, D-16 to D-18).

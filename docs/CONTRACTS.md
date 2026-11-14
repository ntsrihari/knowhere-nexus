# Nexus — Data Contracts

Revision 1 (14 Nov 2026). This covers the two contracts the owner approved: the Phase 1 catalog (**[OWNER B-03]**) and the provisional theme (**[OWNER B-04]**). Field choices beyond those approvals are **[REC]** and listed in DECISIONS.md §C.

## 0. Versioning rules (both contracts) [REC R-17]
- Every document carries `schema` (a fixed name) and `schemaVersion` (`MAJOR.MINOR.PATCH`) **[OWNER requirement 6]**.
- Nexus accepts a document only if `schema` matches exactly and `MAJOR` equals a major version Nexus supports.
- **MINOR increments are additive only**, even while MAJOR is 0. Nexus ignores unknown fields in a newer MINOR, and rejects unknown fields when the version is one it fully knows. This keeps typos from passing silently.
- PATCH carries clarifications only; nothing structural changes.
- A different MAJOR is rejected with `unsupported_schema_version`. Converters handle migrations (for example to Prism's final schema).
- Fields under `extensions` are namespaced (`collector.*`, `prism.*`, `milano.*`). Nexus stores them unchanged and never interprets them.

## 1. Catalog — `knowhere.catalog` 0.1.0

**Purpose:** the server-managed list the launcher and the Services widget read **[OWNER A-09]**.
- In Phase 1 the source is `catalog.yaml` on the Nexus host **[OWNER B-03]**.
- From Phase 2, Collector (which owns the catalog under **[OWNER A-06]**) publishes documents in the same contract.

```yaml
schema: knowhere.catalog
schemaVersion: 0.1.0
revision: 1                    # integer ≥ 1; must increase on every change
source: file                   # file | collector
updatedAt: 2026-11-14T00:00:00Z
apps:
  - id: jellyfin               # ^[a-z][a-z0-9-]{1,39}$, unique, never reused
    name: Jellyfin             # 1–40 characters
    kind: server               # server | android | theme   (kinds from the Platform Plan)
    description: Films and shows   # optional, ≤ 140 characters
    icon: { builtin: jellyfin }    # Phase 1: built-in icon keys only
    audience: all              # all | admin
    launch:
      route: null              # id from the authorized route registry, or null (A-02)
    health:
      containers: [jellyfin]   # container names for the up/down summary; may be empty
    extensions:
      collector: {}            # source repo, upstream, channel, compose ref: owned by Collector
```

**Validation:**
- Maximum 256 KB and 200 apps. YAML anchors and aliases are rejected (no alias-expansion attacks). Duplicate keys are rejected.
- `launch.route` must exist in the route registry, or be `null`.
- `health.containers` entries must match `^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`.
- `revision` must be greater than the loaded revision. If it's equal but the content differs → rejected.
- **Phase 1 display:** only `kind: server` entries appear in the launcher. `android` and `theme` entries are accepted and stored, for Collector compatibility, but not shown.
- **Collector compatibility:** Collector-specific data lives only under `extensions.collector`, so Collector can grow its schema without a Nexus release. Collector must keep `id` stable across revisions.

## 2. Theme — `knowhere.theme` 0.1.0

**Purpose:** a provisional theme contract until Prism publishes its own **[OWNER B-04]**. The fields are exactly the ones approved: seed colour, light/dark preference, corner radius, font, density, optional wallpaper.

```json
{
  "schema": "knowhere.theme",
  "schemaVersion": "0.1.0",
  "name": "Knowhere Default",
  "seedColor": "#6750A4",
  "colorScheme": "dark",
  "cornerRadius": 12,
  "font": "inter",
  "density": "comfortable",
  "wallpaper": null,
  "extensions": {}
}
```

| Field | Type and range | Required | Notes |
|---|---|---|---|
| `name` | string, 1–40 | no | Display only |
| `seedColor` | `^#[0-9A-Fa-f]{6}$` (sRGB) | yes | Input to Material 3 scheme generation |
| `colorScheme` | `light` \| `dark` | yes | A `system` value is **not** included: open item Q-05 |
| `cornerRadius` | integer px, 0–32 | yes | Range is [REC] |
| `font` | key from Nexus's bundled font allowlist | yes | No remote fonts (CSP). Allowlist is [REC]: Q-06 |
| `density` | `comfortable` \| `compact` | yes | |
| `wallpaper` | `null` or `{ "ref": "builtin:<key>" }` | no | Phase 1: built-in wallpapers only; uploads wait for D-06 |
| `extensions` | object of namespaced objects | no | For example `prism.*` and `milano.*`; the Material You toggle is a Milano setting, not part of this contract |

**Validation:** maximum 4 KB; unknown fields handled as in §0.

**Prism compatibility:** this contract is a strict subset of the fields the Platform Plan assigns to Prism. When Prism publishes, Nexus adds a converter from `knowhere.theme` 0.x to Prism's version 1. Stored themes are migrated once, and the original is kept for rollback.

## 3. Where the contracts live
- JSON Schema files will sit in `contracts/` in `knowhere-nexus`, with valid and invalid fixtures, and are the source of truth for validators.
- Once Collector and Prism exist, the schemas move to a shared location (decision LATER D-20).

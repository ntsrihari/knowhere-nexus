# syntax=docker/dockerfile:1
# Knowhere Nexus image: static Go binary with the SvelteKit build embedded,
# on distroless (no shell, no package manager), running as non-root (UID 65532).
# Base images are pinned by digest; Dependabot proposes updates.

ARG NODE_IMAGE=node:22.23.4-bookworm-slim@sha256:5ec23b7c622b00547753bc6ffd78085dd868a68aeb41c8e0a1418f033e13d96d
ARG GO_IMAGE=golang:1.27.4-bookworm@sha256:4fa7c968ba93f1635670522dbb01a2c78150443e1385746924566079f36570ea
ARG RUNTIME_IMAGE=gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab

# --- Frontend -----------------------------------------------------------------
FROM ${NODE_IMAGE} AS web
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json frontend/.npmrc ./
# No install scripts: nothing in the dependency tree needs them to build.
RUN npm ci --ignore-scripts
COPY frontend/ ./
RUN npm run build

# --- Backend ------------------------------------------------------------------
FROM ${GO_IMAGE} AS go
WORKDIR /src
ENV CGO_ENABLED=0 GOTOOLCHAIN=local GOFLAGS=-mod=readonly
COPY go.mod go.sum ./
RUN go mod download && go mod verify
COPY cmd ./cmd
COPY contracts ./contracts
COPY internal ./internal
COPY --from=web /src/frontend/build/ ./internal/webui/dist/
ARG VERSION=dev
ARG COMMIT=unknown
RUN go build -trimpath \
      -ldflags "-s -w -X github.com/ntsrihari/knowhere-nexus/internal/buildinfo.Version=${VERSION} -X github.com/ntsrihari/knowhere-nexus/internal/buildinfo.Commit=${COMMIT}" \
      -o /out/nexus ./cmd/nexus \
 && mkdir -p /out/data && chmod 0700 /out/data

# --- Runtime ------------------------------------------------------------------
FROM ${RUNTIME_IMAGE}
ARG VERSION=dev
ARG COMMIT=unknown
LABEL org.opencontainers.image.title="knowhere-nexus" \
      org.opencontainers.image.source="https://github.com/ntsrihari/knowhere-nexus" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}"
COPY --from=go /out/nexus /nexus
# The only writable path: mount a volume here (SQLite database).
COPY --from=go --chown=65532:65532 /out/data /var/lib/nexus
USER 65532:65532
EXPOSE 8480
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD ["/nexus", "healthcheck", "-addr", "127.0.0.1:8480"]
ENTRYPOINT ["/nexus"]
CMD ["serve", "-config", "/etc/nexus/config.yaml"]

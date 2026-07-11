# syntax=docker/dockerfile:1
# Base image policy: track explicit upstream patch/minor tags (not latest) so
# routine rebuilds pick up maintained Debian package fixes without hiding major
# upgrades. The GitHub Docker workflow emits provenance and SBOM attestations for
# every published image.

FROM node:24.16.0-bookworm-slim AS web-builder
WORKDIR /src/web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
ARG VERSION=dev
ENV VITE_BUILD_ID=${VERSION}
RUN npm run build

FROM golang:1.25.12-bookworm AS go-builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . ./
COPY --from=web-builder /src/web/dist ./web/dist
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o /out/zeno-controller ./cmd/controller

FROM debian:13.2-slim
ARG VERSION=dev
ARG REVISION=unknown
ARG ZENO_UID=10001
ARG ZENO_GID=10001
LABEL org.opencontainers.image.title="Zeno" \
  org.opencontainers.image.description="Lightweight self-hosted server monitor" \
  org.opencontainers.image.source="https://github.com/shuijiao1/Zeno" \
  org.opencontainers.image.version="${VERSION}" \
  org.opencontainers.image.revision="${REVISION}"
RUN apt-get update \
  && apt-get install -y --no-install-recommends ca-certificates curl tzdata \
  && rm -rf /var/lib/apt/lists/* \
  && groupadd --system --gid "${ZENO_GID}" zeno \
  && useradd --system --uid "${ZENO_UID}" --gid zeno --home-dir /opt/zeno --shell /usr/sbin/nologin zeno \
  && mkdir -p /opt/zeno /data \
  && chown -R zeno:zeno /opt/zeno /data
WORKDIR /opt/zeno
COPY --from=go-builder /out/zeno-controller /usr/local/bin/zeno-controller
COPY --from=web-builder /src/web/dist /opt/zeno/web
RUN chown -R zeno:zeno /opt/zeno/web /usr/local/bin/zeno-controller
ENV TZ=Asia/Shanghai
EXPOSE 18980
USER zeno:zeno
ENTRYPOINT ["/usr/local/bin/zeno-controller"]
CMD ["-addr", "0.0.0.0:18980", "-web-dir", "/opt/zeno/web", "-db", "/data/zeno.db", "-admin-token-file", "/run/secrets/zeno_admin_token", "-agent-token-file", "/run/secrets/zeno_agent_token"]

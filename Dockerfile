# syntax=docker/dockerfile:1
# Base image policy: track explicit upstream patch/minor tags (not latest) so
# routine rebuilds pick up maintained Debian package fixes without hiding major
# upgrades. The GitHub Docker workflow emits provenance and SBOM attestations for
# every published image.

FROM --platform=$BUILDPLATFORM node:24.16.0-bookworm-slim@sha256:2c87ef9bd3c6a3bd4b472b4bec2ce9d16354b0c574f736c476489d09f560a203 AS web-builder
WORKDIR /src/web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
ARG VERSION=dev
ENV VITE_BUILD_ID=${VERSION}
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.25.12-bookworm@sha256:a9c020ee3d1508c7be5435c262434e3d3fc1d0e76a11afeb9ddae7d60bc86aa4 AS go-builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . ./
COPY --from=web-builder /src/web/dist ./web/dist
ARG VERSION=dev
ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags "-s -w" -o /out/zeno-controller ./cmd/controller

FROM debian:13.2-slim@sha256:4bcb9db66237237d03b55b969271728dd3d955eaaa254b9db8a3db94550b1885
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
  && apt-get install -y --no-install-recommends ca-certificates curl iputils-ping tzdata \
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
CMD ["-addr", "0.0.0.0:18980", "-web-dir", "/opt/zeno/web", "-db", "/data/zeno.db", "-admin-token-file", "/run/secrets/zeno_admin_token", "-agent-token-file", "/run/secrets/zeno_agent_token", "-notification-authority-key-file", "/run/secrets/zeno_notification_authority"]

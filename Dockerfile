# syntax=docker/dockerfile:1

# The interface is built first, in its own stage, so a change to Go source does
# not reinstall node modules and vice versa.
FROM node:22-alpine AS web
WORKDIR /src/web
RUN corepack enable
COPY web/package.json web/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
COPY internal/web/dist/.gitkeep /src/internal/web/dist/.gitkeep
RUN pnpm run build

FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/internal/web/dist/ ./internal/web/dist/

# TARGETARCH is supplied by buildx, which is what makes one Dockerfile produce
# both amd64 and arm64 images. CGO stays off, so cross-compiling needs no
# toolchain for the target.
ARG TARGETOS TARGETARCH VERSION=
# When no version is passed in (the Dokploy build-from-source path does not),
# fall back to version.txt — the release-please source of truth — so the running
# instance still reports the version it was built at rather than "dev".
RUN VERSION="${VERSION:-v$(cat version.txt 2>/dev/null || echo 0.0.0)}" && \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/zefile ./cmd/zefile

# Alpine rather than scratch or distroless, for one reason: honouring PUID and
# PGID needs a shell and su-exec at start-up. A static image would force whoever
# deploys to chown the host directory to a user id they did not choose, which is
# the first thing that goes wrong in self-hosting.
FROM alpine:3.21
RUN apk add --no-cache su-exec tzdata ca-certificates && \
    addgroup -g 1000 zefile && \
    adduser -D -u 1000 -G zefile zefile

COPY --from=build /out/zefile /usr/local/bin/zefile
COPY deploy/entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh

ENV ZEFILE_DATA_DIR=/data \
    ZEFILE_CONFIG_DIR=/config \
    ZEFILE_LISTEN=:8080
VOLUME ["/data", "/config"]
EXPOSE 8080

# The interval is generous: a health check that runs every few seconds on a
# machine busy serving a large download is a cost, not a diagnostic.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -qO- http://127.0.0.1:8080/healthz >/dev/null || exit 1

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
CMD ["zefile"]

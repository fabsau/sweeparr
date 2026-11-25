# syntax=docker/dockerfile:1

FROM --platform=$BUILDPLATFORM node:26-alpine AS web-build
WORKDIR /src/web
COPY --link web/package.json web/package-lock.json web/.npmrc ./
RUN --mount=type=cache,target=/root/.npm \
    npm ci
COPY --link web/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS go-build
ARG TARGETOS=linux
ARG TARGETARCH=amd64
ARG VERSION=0.9.2
ARG COMMIT=""
ARG BUILD_DATE=""
WORKDIR /src
COPY --link go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download
COPY --link cmd/ ./cmd/
COPY --link internal/ ./internal/
COPY --link embed.go ./
COPY --link --from=web-build /src/web/dist ./web/dist
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -buildvcs=false \
    -ldflags="-s -w -X github.com/fabsau/sweeparr/internal/version.Version=${VERSION} -X github.com/fabsau/sweeparr/internal/version.Commit=${COMMIT} -X github.com/fabsau/sweeparr/internal/version.BuildDate=${BUILD_DATE}" \
    -o /out/sweeparr ./cmd/sweeparr

FROM alpine:3.24
ARG VERSION=0.9.2
ARG COMMIT=""
ARG BUILD_DATE=""

LABEL org.opencontainers.image.title="Sweeparr" \
      org.opencontainers.image.description="Programmatic media analysis orchestrator for Plex servers" \
      org.opencontainers.image.url="https://github.com/fabsau/sweeparr" \
      org.opencontainers.image.source="https://github.com/fabsau/sweeparr" \
      org.opencontainers.image.documentation="https://github.com/fabsau/sweeparr#readme" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}" \
      org.opencontainers.image.created="${BUILD_DATE}" \
      org.opencontainers.image.licenses="MIT"

RUN apk add --no-cache ca-certificates tzdata su-exec \
    && addgroup -S -g 1000 sweeparr \
    && adduser -S -u 1000 -G sweeparr sweeparr \
    && mkdir -p /state \
    && chown sweeparr:sweeparr /state

WORKDIR /app
COPY --link --chmod=0755 --from=go-build /out/sweeparr /app/sweeparr
COPY --link --chmod=0755 docker-entrypoint.sh /app/entrypoint.sh

ENV SWEEPARR_PORT=8080 \
    SWEEPARR_STATE_DIR=/state \
    PUID=1000 \
    PGID=1000

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD ["/app/sweeparr", "-healthcheck"]

ENTRYPOINT ["/app/entrypoint.sh"]

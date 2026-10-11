# syntax=docker/dockerfile:1

# 1. Build the user interface.
FROM --platform=$BUILDPLATFORM node:22-alpine AS ui
WORKDIR /src/ui
COPY ui/package.json ui/package-lock.json ./
RUN npm ci
COPY ui/ ./
RUN npm run build

# 2. Build the server, with the user interface compiled in. These two stages run on the
#    builder's own architecture and cross-compile, so multi-platform builds need no emulation.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS server
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
COPY --from=ui /src/internal/web/dist/ internal/web/dist/
ARG VERSION=dev
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /regix ./cmd/regix

# 3. The image: one static binary, CA certificates, no shell, not root.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=server /regix /regix
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s CMD ["/regix", "healthcheck"]
LABEL org.opencontainers.image.title="Regix" \
      org.opencontainers.image.description="A web interface for a self-hosted Docker / OCI registry" \
      org.opencontainers.image.licenses="MIT"
ENTRYPOINT ["/regix"]

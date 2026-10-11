# syntax=docker/dockerfile:1

# 1. Build the user interface.
FROM node:22-alpine AS ui
WORKDIR /src/ui
COPY ui/package.json ui/package-lock.json ./
RUN npm ci
COPY ui/ ./
RUN npm run build

# 2. Build the server, with the user interface compiled in.
FROM golang:1.26-alpine AS server
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
COPY --from=ui /src/internal/web/dist/ internal/web/dist/
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /regix ./cmd/regix

# 3. The image: one static binary, CA certificates, no shell, not root.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=server /regix /regix
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s CMD ["/regix", "healthcheck"]
ENTRYPOINT ["/regix"]

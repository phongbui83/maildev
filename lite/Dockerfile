# syntax=docker/dockerfile:1
# Build from the repository root: docker build -f lite/Dockerfile .
# One image: the Go binary with the static web UI embedded. No database, no
# Node.js at runtime; state lives in /data (SQLite), the certificate in /certs.

FROM node:24-alpine AS web
WORKDIR /web
COPY frontend/package.json frontend/package-lock.json* ./
RUN npm ci --no-audit --no-fund
COPY frontend/ .
ENV NEXT_OUTPUT=export NEXT_TELEMETRY_DISABLED=1
RUN npm run build

FROM golang:1.26-alpine AS build
ARG VERSION=dev
WORKDIR /src
COPY lite/go.mod lite/go.sum ./
RUN go mod download
COPY lite/ .
RUN rm -rf internal/webui/dist
COPY --from=web /web/out ./internal/webui/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/maildev-lite ./cmd/maildev-lite

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata && adduser -S -u 10001 maildev \
    && mkdir -p /data /certs && chown 10001 /data /certs
COPY --from=build /out/maildev-lite /usr/local/bin/maildev-lite
USER 10001
VOLUME ["/data"]
EXPOSE 8080 8443 1025 1587 1465 1143 1993 1110 1995
HEALTHCHECK --interval=30s --timeout=5s CMD wget -qO- http://127.0.0.1:8080/readyz || exit 1
ENTRYPOINT ["/usr/local/bin/maildev-lite"]

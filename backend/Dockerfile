# syntax=docker/dockerfile:1
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/maildev ./cmd/maildev

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata && adduser -S -u 10001 maildev
COPY --from=build /out/maildev /usr/local/bin/maildev
USER 10001
EXPOSE 8080 25 465 587 143 993
ENTRYPOINT ["/usr/local/bin/maildev"]

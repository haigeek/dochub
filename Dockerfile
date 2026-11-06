FROM node:24-alpine AS web-builder
WORKDIR /src/web
RUN npm install --global pnpm@11.25.0
COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm build

FROM golang:1.25-alpine AS server-builder
WORKDIR /src/server
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/dochub ./cmd/dochub

FROM alpine:3.22
RUN apk add --no-cache ca-certificates git openssh-client su-exec tzdata \
    && addgroup -S -g 10001 dochub \
    && adduser -S -D -H -u 10001 -G dochub dochub \
    && mkdir -p /app/web /data /run/dochub-ssh \
    && chown -R dochub:dochub /data
WORKDIR /app
COPY --from=server-builder /out/dochub /app/dochub
COPY --from=web-builder /src/web/dist /app/web
COPY deploy/entrypoint.sh /usr/local/bin/dochub-entrypoint
RUN chmod 0755 /usr/local/bin/dochub-entrypoint \
    && chown -R dochub:dochub /app

ENV APP_ADDR=:8080 \
    WEB_DIR=/app/web \
    STORAGE_DIR=/data
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/dochub-entrypoint"]
CMD ["/app/dochub"]

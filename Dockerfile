# syntax=docker/dockerfile:1.7
FROM node:22-alpine AS web
RUN corepack enable
WORKDIR /app/frontend
COPY frontend/package.json frontend/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY frontend/ ./
RUN pnpm build

FROM golang:1.27-alpine AS api
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/anyquery ./cmd/server \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/anyquery-seed ./cmd/seed

FROM debian:bookworm-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates tzdata mdbtools \
 && rm -rf /var/lib/apt/lists/* \
 && useradd --system --uid 10001 --home /app --shell /usr/sbin/nologin anyquery \
 && mkdir -p /app/data /app/web
WORKDIR /app
COPY --from=api /out/anyquery /app/anyquery
COPY --from=api /out/anyquery-seed /app/anyquery-seed
COPY --from=web /app/frontend/dist /app/web
COPY docker-entrypoint.sh /app/docker-entrypoint.sh
RUN chmod +x /app/docker-entrypoint.sh && chown -R anyquery:anyquery /app
ENV WEB_DIR=/app/web
USER anyquery
EXPOSE 8080
ENTRYPOINT ["/app/docker-entrypoint.sh"]

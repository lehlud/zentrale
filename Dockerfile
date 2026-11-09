# syntax=docker/dockerfile:1

FROM node:22-bookworm-slim AS frontend
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci
COPY frontend/ ./
RUN npm run build

FROM golang:1.27.3-bookworm AS backend
RUN apt-get update \
    && apt-get install -y --no-install-recommends g++ \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /src/backend
COPY backend/go.mod backend/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY backend/ ./
# DuckDB uses CGO and links against the system C++ runtime.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o /out/zentrale .

FROM debian:bookworm-slim AS runtime
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates libstdc++6 \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid 10001 zentrale \
    && useradd --uid 10001 --gid zentrale --home-dir /data \
        --no-create-home --shell /usr/sbin/nologin zentrale \
    && install -d -o zentrale -g zentrale -m 0700 /data
WORKDIR /app
COPY --from=backend /out/zentrale /usr/local/bin/zentrale
COPY --from=frontend /src/frontend/dist/ ./frontend/
ENV ZENTRALE_VSCODE_CACHE=/data/cache/vscode
USER 10001:10001
VOLUME ["/data"]
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/zentrale", "-addr", ":8080", "-db", "/data/zentrale.duckdb", "-frontend", "/app/frontend"]

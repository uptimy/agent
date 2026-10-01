# syntax=docker/dockerfile:1

# 1. Build the React UI.
FROM node:22-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# 2. Build a static Go binary with the UI embedded.
FROM golang:1.27-alpine AS go
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/uptimy-agent ./cmd/uptimy-agent \
 && mkdir -p /out/data

# 3. Minimal runtime: no shell, no package manager, runs as non-root.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=go /out/uptimy-agent /usr/local/bin/uptimy-agent
COPY --from=go --chown=65532:65532 /out/data /data
ENV PORT=8080 DATA_DIR=/data
EXPOSE 8080
VOLUME /data
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s CMD ["/usr/local/bin/uptimy-agent", "healthcheck"]
ENTRYPOINT ["/usr/local/bin/uptimy-agent"]

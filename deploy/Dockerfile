FROM node:24-bookworm AS web-build
WORKDIR /app/web
COPY web/package.json web/package-lock.json* ./
RUN npm install --include=dev --no-audit --no-fund
COPY web ./
RUN npm run build

FROM golang:1.26-bookworm AS go-build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . ./
COPY --from=web-build /app/web/dist ./web/dist
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/roughdashd ./cmd/roughdashd
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/roughdash-helper ./cmd/roughdash-helper

FROM --platform=linux/amd64 debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
  ca-certificates \
  curl \
  ffmpeg \
  nodejs \
  sqlite3 \
  unzip \
  python3 \
  python3-pip \
  && rm -rf /var/lib/apt/lists/*
RUN python3 -m pip install --break-system-packages yt-dlp
ENV DENO_INSTALL=/usr/local
RUN curl -fsSL https://deno.land/install.sh | sh && deno --version
WORKDIR /app
COPY --from=go-build /out/roughdashd /usr/local/bin/roughdashd
COPY --from=go-build /app/web/dist /app/web/dist
ENV ROUGHDASH_ADDR=:8420
ENV ROUGHDASH_DATA_DIR=/data
ENV ROUGHDASH_DB_PATH=/data/roughdash.sqlite
ENV ROUGHDASH_TEMP_DIR=/data/tmp
ENV ROUGHDASH_STATIC_DIR=/app/web/dist
ENV ROUGHDASH_NAS_ROOT=/mnt/Main/AIDEN
ENV ROUGHDASH_BOOTSTRAP_SECRET=roughdash-bootstrap
EXPOSE 8420
VOLUME ["/data", "/mnt/Main/AIDEN"]
CMD ["roughdashd"]

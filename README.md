# roughdash

`roughdash` is a single-user TrueNAS-oriented dashboard for media ingest and YouTube download operations.

## What is implemented

- Go backend monolith with:
  - bootstrap setup
  - password login
  - SQLite persistence
  - audit log
  - helper pairing and remote browse
  - ingest preview and local NAS ingest jobs
  - yt-dlp preview and download/transcode jobs
  - job queue with pause/resume/cancel
  - export and notification settings endpoints
- Go helper agent for `macOS` and `Windows` style pairing flow
- React/Vite dashboard shell with pages for:
  - setup/login
  - dashboard
  - ingest
  - downloads
  - helpers
  - jobs
  - settings/audit

## Current limitation

Remote helper file transfer is not wired into ingest execution yet. Helper pairing and helper browsing are implemented; ingest execution currently accepts NAS-local sources only.

## Local development

Backend:

```bash
go run ./cmd/roughdashd
```

Frontend:

```bash
cd web
npm install
npm run dev
```

The Vite dev server proxies `/api` and `/ws` traffic to `http://localhost:8420`.

## Environment

Important backend environment variables:

```bash
ROUGHDASH_ADDR=:8420
ROUGHDASH_DATA_DIR=./data
ROUGHDASH_DB_PATH=./data/roughdash.sqlite
ROUGHDASH_TEMP_DIR=./data/tmp
ROUGHDASH_STATIC_DIR=./web/dist
ROUGHDASH_NAS_ROOT=/mnt/Main/AIDEN
ROUGHDASH_BOOTSTRAP_SECRET=roughdash-bootstrap
```

## Helper usage

First-time pairing:

```bash
go run ./cmd/roughdash-helper --server http://localhost:8420 --approve
```

The helper prints a one-time pairing code. Enter that code in the `Helpers` page, and the helper will persist its token locally.

## Build verification

Verified in this workspace:

```bash
go test ./...
```

Frontend build verification depends on a clean `npm install` in `web/`.

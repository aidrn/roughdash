APP_NAME := roughdash

.PHONY: run test web-build helper

run:
	go run ./cmd/roughdashd

test:
	go test ./...

web-build:
	cd web && npm run build

helper:
	go run ./cmd/roughdash-helper --server http://localhost:8420 --approve

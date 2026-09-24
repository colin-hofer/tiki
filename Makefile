.DEFAULT_GOAL := build
.PHONY: dev frontend cli check fmt test vet build bench

dev: frontend/node_modules/.tiki-installed
	npm --prefix frontend run dev

frontend/node_modules/.tiki-installed: frontend/package.json frontend/package-lock.json
	npm --prefix frontend ci --no-audit --no-fund
	touch $@

frontend: frontend/node_modules/.tiki-installed
	npm --prefix frontend run build

cli:
	sh scripts/build-cli.sh

check: fmt test vet build
	bash -n scripts/deploy.sh scripts/install-server.sh

fmt:
	@test -z "$$(gofmt -l main.go internal frontend/*.go)" || { gofmt -l main.go internal frontend/*.go; exit 1; }

test: frontend cli
	go test -race ./...

vet: frontend cli
	go vet ./...

build: frontend cli
	go build -o tiki .

bench:
	go test ./internal/tiki -run '^$$' -bench . -benchmem -count=3

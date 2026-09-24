.DEFAULT_GOAL := build
.PHONY: dev frontend check fmt test vet build bench

dev: frontend/node_modules/.tiki-installed
	npm --prefix frontend run dev

frontend/node_modules/.tiki-installed: frontend/package.json frontend/package-lock.json
	npm --prefix frontend ci --no-audit --no-fund
	touch $@

frontend: frontend/node_modules/.tiki-installed
	npm --prefix frontend run build

check: fmt test vet build

fmt:
	@test -z "$$(gofmt -l main.go internal frontend/*.go)" || { gofmt -l main.go internal frontend/*.go; exit 1; }

test: frontend
	go test -race ./...

vet: frontend
	go vet ./...

build: frontend
	go build -o tiki .

bench:
	go test ./internal/tiki -run '^$$' -bench . -benchmem -count=3

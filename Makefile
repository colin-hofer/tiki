.DEFAULT_GOAL := build
.PHONY: dev frontend cli check fmt format lint test test-go test-web vet build bench

dev: frontend/node_modules/.tiki-installed
	npm --prefix frontend run dev

frontend/node_modules/.tiki-installed: frontend/package.json frontend/package-lock.json
	npm --prefix frontend ci --no-audit --no-fund
	touch $@

frontend: frontend/node_modules/.tiki-installed
	node scripts/build-frontend.mjs

cli:
	sh scripts/build-cli.sh

check: fmt lint test vet build
	bash -n scripts/*.sh
	python3 scripts/test-deploy.py

fmt: frontend/node_modules/.tiki-installed
	@test -z "$$(gofmt -l main.go internal frontend/*.go skills/*.go)" || { gofmt -l main.go internal frontend/*.go skills/*.go; exit 1; }
	npm --prefix frontend run format:check

format: frontend/node_modules/.tiki-installed
	gofmt -w main.go internal frontend/*.go skills/*.go
	npm --prefix frontend run format

lint: frontend/node_modules/.tiki-installed
	npm --prefix frontend run lint

test: test-go test-web

test-go: frontend cli
	go test -race ./...

test-web: frontend
	npm --prefix frontend test

vet: frontend cli
	go vet ./...

build: frontend cli
	go build -o tiki .

bench:
	go test ./internal/tiki -run '^$$' -bench . -benchmem -count=3

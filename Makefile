.PHONY: check fmt test vet build bench

check: fmt test vet build

fmt:
	@test -z "$$(gofmt -l main.go internal)" || { gofmt -l main.go internal; exit 1; }

test:
	go test -race ./...

vet:
	go vet ./...

build:
	go build -o tiki .

bench:
	go test ./internal/tiki -run '^$$' -bench . -benchmem -count=3

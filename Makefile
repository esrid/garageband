.PHONY: dev build test migrate generate vet

generate:
	go tool templ generate

migrate:
	go run ./cmd/migrate

dev: generate migrate
	go run ./cmd/web

build: generate
	go build -o bin/web ./cmd/web
	go build -o bin/migrate ./cmd/migrate

test: generate
	go test ./...

vet: generate
	go vet ./...

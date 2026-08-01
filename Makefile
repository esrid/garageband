.PHONY: dev build test migrate generate templ css vet

# Tailwind CSS 4 + daisyUI 5 via the standalone CLI: no Node, no package.json.
# The tools live in the gitignored bin/ and are fetched once, on demand.
# Versions are pinned: "latest" would silently change the generated CSS the day
# a new major ships. Bump them deliberately, then commit the rebuilt app.css.
TAILWIND_VERSION := v4.3.3
DAISYUI_VERSION := v5.7.9
TAILWIND_DIR := bin/tailwind
TAILWIND := $(TAILWIND_DIR)/tailwindcss
TAILWIND_OS := $(shell uname -s | tr '[:upper:]' '[:lower:]' | sed 's/darwin/macos/')
TAILWIND_ARCH := $(shell uname -m | sed -e 's/x86_64/x64/' -e 's/aarch64/arm64/')

generate: templ css

templ:
	go tool templ generate

$(TAILWIND):
	mkdir -p $(TAILWIND_DIR)
	curl -sSfL -o $@ https://github.com/tailwindlabs/tailwindcss/releases/download/$(TAILWIND_VERSION)/tailwindcss-$(TAILWIND_OS)-$(TAILWIND_ARCH)
	chmod +x $@
	curl -sSfL -o $(TAILWIND_DIR)/daisyui.mjs https://github.com/saadeghi/daisyui/releases/download/$(DAISYUI_VERSION)/daisyui.mjs
	curl -sSfL -o $(TAILWIND_DIR)/daisyui-theme.mjs https://github.com/saadeghi/daisyui/releases/download/$(DAISYUI_VERSION)/daisyui-theme.mjs

css: $(TAILWIND)
	$(TAILWIND) -i web/css/app.css -o web/static/app.css --minify

migrate:
	go run ./cmd/migrate

dev: generate migrate
	go run ./cmd/web

build: generate
	go build -o bin/web ./cmd/web
	go build -o bin/migrate ./cmd/migrate

# test/vet need templ only: a fresh clone must not require the network just to
# run the suite, and web/static/app.css is committed.
test: templ
	go test ./...

vet: templ
	go vet ./...

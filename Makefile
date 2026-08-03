.PHONY: dev build test migrate generate templ css vet vendor-unpoly

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

# Unpoly: progressive-enhancement layer for links/forms (modals, in-place
# swaps). Vendored and committed to web/static/, same treatment as app.css.
# Verified against docs.unpoly.com 2026-08-03. Bump deliberately.
UNPOLY_VERSION := 3.14.3

generate: templ css

templ:
	go tool templ generate

$(TAILWIND):
	mkdir -p $(TAILWIND_DIR)
	curl -sSfL -o $@ https://github.com/tailwindlabs/tailwindcss/releases/download/$(TAILWIND_VERSION)/tailwindcss-$(TAILWIND_OS)-$(TAILWIND_ARCH)
	chmod +x $@
	curl -sSfL -o $(TAILWIND_DIR)/daisyui.mjs https://github.com/saadeghi/daisyui/releases/download/$(DAISYUI_VERSION)/daisyui.mjs

css: $(TAILWIND)
	$(TAILWIND) -i web/css/app.css -o web/static/app.css --minify

vendor-unpoly:
	curl -sSfL -o web/static/unpoly.min.js https://cdn.jsdelivr.net/npm/unpoly@$(UNPOLY_VERSION)/unpoly.min.js
	curl -sSfL -o web/static/unpoly.min.css https://cdn.jsdelivr.net/npm/unpoly@$(UNPOLY_VERSION)/unpoly.min.css

migrate:
	go run ./cmd/migrate

dev: generate migrate
	go run ./cmd/web

build: generate
	go build -o bin/web ./cmd/web
	go build -o bin/migrate ./cmd/migrate

# test/vet need templ only: web/static/app.css is committed. The test command
# starts one PostgreSQL 18 container for the complete Go suite, then each
# database test keeps using its own isolated schema inside that instance.
test: templ
	go run ./cmd/test ./...

vet: templ
	go vet ./...

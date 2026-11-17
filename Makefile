VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
PLATFORMS := amd64 arm64 arm 386
SHOTS := .shots

.PHONY: build test install release screenshots

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o wgtui .

test:
	go vet ./...
	go test ./...

install: build
	install -Dm755 wgtui $(DESTDIR)/usr/local/bin/wgtui

release:
	mkdir -p dist
	for a in $(PLATFORMS); do \
		CGO_ENABLED=0 GOOS=linux GOARCH=$$a go build -trimpath -ldflags "$(LDFLAGS)" -o dist/wgtui-linux-$$a . ; \
	done
	cd dist && sha256sum wgtui-linux-* > SHA256SUMS

# Renders docs/screenshots/*.png from demo mode. Needs charmbracelet/freeze.
screenshots:
	mkdir -p $(SHOTS) docs/screenshots
	WGTUI_SCREENSHOTS=$(CURDIR)/$(SHOTS) go test ./internal/ui -run Screenshots -count=1
	for f in $(SHOTS)/*.ansi; do \
		freeze $$f --language ansi --window --padding 20 --border.radius 8 --background "#1A1B26" \
			--font.size 14 --line-height 1.2 --output docs/screenshots/$$(basename $$f .ansi).png ; \
	done

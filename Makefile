VERSION ?= 0.2.3-dev
MODULE  := github.com/Amitgb14/conch
COMMIT  := $(shell git rev-parse HEAD 2>/dev/null)
LDFLAGS := -X $(MODULE)/internal/proto.Version=$(VERSION) -X $(MODULE)/internal/buildinfo.Commit=$(COMMIT)
# Pinned, so a new staticcheck release can't turn CI red on its own. The
# checks it leaves out, and why, are in staticcheck.conf.
STATICCHECK := honnef.co/go/tools/cmd/staticcheck@2025.1.1

.PHONY: build conch-sandbox sandbox-release install test test-fast gate vet lint release app-release clean demo settings-ui settings-ui-check conch-ui conch-ui-check desktop-test

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/conch ./cmd/conch

# conch-sandbox: conch's provider code alone, for herdr, the herdr app and
# Orca (cmd/conch-sandbox). Its version is the launcher's to compare.
conch-sandbox:
	go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o bin/conch-sandbox ./cmd/conch-sandbox

install:
	go install -trimpath -ldflags "$(LDFLAGS)" ./cmd/conch

test:
	go test -race -timeout 20m ./...

# The same suite as `go test -race -count=1 -timeout 20m ./...`, with
# internal/server — ten minutes of tests that run one at a time — split
# across processes (scripts/test.sh says why it is safe). SHARDS=N to change.
test-fast:
	scripts/test.sh

# What has to be green before a push: lint, then the whole suite.
gate: lint test-fast

vet:
	go vet ./...

# What CI checks before the tests: formatting, vet and staticcheck. Run on
# macOS and Linux both, since each builds files the other doesn't.
lint:
	@unformatted=$$(gofmt -l cmd internal); \
	if [ -n "$$unformatted" ]; then echo "not gofmt-formatted (run gofmt -w):"; echo "$$unformatted"; exit 1; fi
	go vet ./...
	go run $(STATICCHECK) ./...

# The settings page (internal/configweb/app: React, Tailwind, shadcn). Its
# build is committed in internal/configweb/dist, so conch builds with go
# alone; run this after changing the app and commit what it writes.
settings-ui:
	cd internal/configweb/app && pnpm install --frozen-lockfile && pnpm build

# What CI runs: rebuild the page and fail if dist/ moved — app/ changed
# without settings-ui being run, or dist/ was edited by hand. The build is
# reproducible, so an unchanged app/ leaves dist/ byte for byte as it was.
settings-ui-check: settings-ui
	@changed=$$(git status --porcelain -- internal/configweb/dist); \
	if [ -n "$$changed" ]; then echo "internal/configweb/dist is not the build of app/: run make settings-ui and commit what it writes"; echo "$$changed"; exit 1; fi

# conch-ui (ui/: React), the views the desktop app draws with HTML. Its
# build is committed as Swift (macos/Sources/ConchKit/ConchUIBundle.swift),
# so Conch.app builds with swift alone; run this after changing ui/ and
# commit what it writes.
conch-ui:
	cd ui && pnpm install --frozen-lockfile && pnpm test && pnpm build

# What CI runs: the logic's tests, a rebuild, and a failure if the
# committed bundle is not the build of ui/.
conch-ui-check: conch-ui
	@changed=$$(git status --porcelain -- macos/Sources/ConchKit/ConchUIBundle.swift); \
	if [ -n "$$changed" ]; then echo "macos/Sources/ConchKit/ConchUIBundle.swift is not the build of ui/: run make conch-ui and commit what it writes"; echo "$$changed"; exit 1; fi

# The Linux and Windows app (desktop/: Electron): its parts that need no
# window, tested with node. `cd desktop && pnpm start` runs it.
desktop-test:
	cd desktop && pnpm install --frozen-lockfile && pnpm test

release:
	scripts/release.sh $(VERSION)

# conch-sandbox for darwin and linux, arm64 and amd64, SHA256SUMS and the
# launcher with them pinned, into dist/ beside release's archives.
sandbox-release:
	scripts/sandbox-release.sh $(VERSION)

# Conch.app for others to download (the desktop beta): dist/Conch_<v>_macos_arm64.zip.
# NOTARY=<notarytool profile> notarizes it; a Developer ID in the keychain signs it.
app-release:
	CONCH_NOTARY_PROFILE=$(NOTARY) macos/release.sh $(VERSION)

# Watch the live diff work, on a throwaway repo with a fake agent editing it.
demo:
	scripts/live-diff-demo.sh

clean:
	rm -rf bin dist

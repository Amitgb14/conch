VERSION ?= 0.1.10-dev
MODULE  := github.com/Amitgb14/conch
LDFLAGS := -X $(MODULE)/internal/proto.Version=$(VERSION)
# Pinned, so a new staticcheck release can't turn CI red on its own. The
# checks it leaves out, and why, are in staticcheck.conf.
STATICCHECK := honnef.co/go/tools/cmd/staticcheck@2025.1.1

.PHONY: build install test test-fast gate vet lint release app-release clean demo settings-ui settings-ui-check

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/conch ./cmd/conch

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

release:
	scripts/release.sh $(VERSION)

# Conch.app for others to download (the desktop beta): dist/Conch_<v>_macos_arm64.zip.
# NOTARY=<notarytool profile> notarizes it; a Developer ID in the keychain signs it.
app-release:
	CONCH_NOTARY_PROFILE=$(NOTARY) macos/release.sh $(VERSION)

# Watch the live diff work, on a throwaway repo with a fake agent editing it.
demo:
	scripts/live-diff-demo.sh

clean:
	rm -rf bin dist

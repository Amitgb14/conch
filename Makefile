VERSION ?= 0.1.9-dev
MODULE  := github.com/Amitgb14/conch
LDFLAGS := -X $(MODULE)/internal/proto.Version=$(VERSION)

.PHONY: build install test vet release clean demo settings-ui

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/conch ./cmd/conch

install:
	go install -trimpath -ldflags "$(LDFLAGS)" ./cmd/conch

test:
	go test -race -timeout 20m ./...

vet:
	go vet ./...

# The settings page (internal/configweb/app: React, Tailwind, shadcn). Its
# build is committed in internal/configweb/dist, so conch builds with go
# alone; run this after changing the app and commit what it writes.
settings-ui:
	cd internal/configweb/app && pnpm install --frozen-lockfile && pnpm build

release:
	scripts/release.sh $(VERSION)

# Watch the live diff work, on a throwaway repo with a fake agent editing it.
demo:
	scripts/live-diff-demo.sh

clean:
	rm -rf bin dist

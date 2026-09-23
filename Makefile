VERSION ?= 0.1.3-dev
MODULE  := github.com/Amitgb14/conch
LDFLAGS := -X $(MODULE)/internal/proto.Version=$(VERSION)

.PHONY: build install test vet release clean demo

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/conch ./cmd/conch

install:
	go install -trimpath -ldflags "$(LDFLAGS)" ./cmd/conch

test:
	go test -race ./...

vet:
	go vet ./...

release:
	scripts/release.sh $(VERSION)

# Watch the live diff work, on a throwaway repo with a fake agent editing it.
demo:
	scripts/live-diff-demo.sh

clean:
	rm -rf bin dist

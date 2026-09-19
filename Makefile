VERSION ?= 0.1.3-dev
MODULE  := github.com/Amitgb14/conch
LDFLAGS := -X $(MODULE)/internal/proto.Version=$(VERSION)

.PHONY: build install test vet release clean

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

clean:
	rm -rf bin dist

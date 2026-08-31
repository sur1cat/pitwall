BIN     := pitwall
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build install test vet fmt clean release-snapshot bar

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BIN) .

install:
	CGO_ENABLED=0 go install -trimpath -ldflags "$(LDFLAGS)" .

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -l -w .

clean:
	rm -rf bin dist

release-snapshot:
	goreleaser release --snapshot --clean

bar: build
	./bar/build.sh
	@echo "run it with: open bar/build/PitwallBar.app"

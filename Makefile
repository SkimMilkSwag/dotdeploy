GO ?= go

.PHONY: build test vet clean

build:
	$(GO) build -o bin/dotdeploy ./cmd/dotdeploy

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

clean:
	rm -rf bin dist

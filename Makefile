BINARY=bin/dockhand

.PHONY: build test docker-build clean package

build:
	mkdir -p bin
	go build -o $(BINARY) ./cmd/dockhand

test:
	go test ./...

docker-build:
	docker build -t dockhand:local .

package:
	goreleaser release --snapshot --clean --skip=publish

clean:
	rm -rf $(BINARY) dist/

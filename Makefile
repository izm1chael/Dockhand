BINARY=bin/dockhand

.PHONY: build test docker-build clean

build:
	mkdir -p bin
	go build -o $(BINARY) ./cmd/dockhand

test:
	go test ./...

docker-build:
	docker build -t dockhand:local .

clean:
	rm -rf $(BINARY)

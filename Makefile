.PHONY: build test install
build:
	go build -ldflags "-s -w" -o dualhot ./cmd/dualhot
test:
	go vet ./... && go test ./...
install: build
	sudo install -m 0755 dualhot /usr/local/bin/dualhot

.PHONY: build test lint run clean

build:
	go build ./...

test:
	go test ./...

lint:
	go vet ./...
	@fmt_out=$$(gofmt -l .); if [ -n "$$fmt_out" ]; then echo "gofmt needed:"; echo "$$fmt_out"; exit 1; fi

run:
	go run ./cmd/server

clean:
	rm -rf bin/

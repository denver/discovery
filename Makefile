.PHONY: build test lint lint-openapi run clean

build:
	go build ./...

test:
	go test ./...

lint:
	go vet ./...
	@fmt_out=$$(gofmt -l .); if [ -n "$$fmt_out" ]; then echo "gofmt needed:"; echo "$$fmt_out"; exit 1; fi

lint-openapi:
	npx -y @redocly/cli@latest lint openapi/openapi.yaml

run:
	go run ./cmd/server

clean:
	rm -rf bin/

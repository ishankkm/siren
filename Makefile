BIN     := bin/siren
PKG     := ./...
GOOS    ?= linux
GOARCH  ?= amd64
LDFLAGS := -s -w

.PHONY: build build-linux test vet tidy run clean

build:
	go build -ldflags '$(LDFLAGS)' -o $(BIN) ./cmd/siren

build-linux:
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=0 \
		go build -ldflags '$(LDFLAGS)' -o $(BIN) ./cmd/siren

test:
	go test -race -count=1 $(PKG)

vet:
	go vet $(PKG)

tidy:
	go mod tidy

run:
	go run ./cmd/siren -config ./siren.yaml

clean:
	rm -rf bin dist

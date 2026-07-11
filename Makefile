.PHONY: all
all: vet test build

.PHONY: vet
vet:
	go vet ./...

.PHONY: test
test:
	go test -v $(TEST_OPTS) ./...

.PHONY: build
build:
	go build ./cmd/ddmcproxy

.PHONY: install
install:
	go install ./cmd/ddmcproxy

.PHONY: lint
lint:
	golangci-lint run

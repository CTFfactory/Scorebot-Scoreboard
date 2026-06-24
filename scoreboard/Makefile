COVERAGE_THRESHOLD ?= 99
COMPLEXITY_THRESHOLD ?= 6
BUILD_OUTPUT ?= ../bin/scoreboard
VERSION ?= $(shell date +%F)_$(shell git rev-parse --short HEAD 2>/dev/null || echo non-git)

.PHONY: build test race coverage complexity vuln lint audit binary-test

build:
	mkdir -p $(dir $(BUILD_OUTPUT))
	go build -trimpath -buildvcs=false -ldflags "-s -w -X github.com/CTFfactory/Scorebot-Scoreboard/scoreboard.version=$(VERSION)" -o $(BUILD_OUTPUT) ./cmd/main.go

test:
	go test ./...

race:
	go test -race ./...

coverage:
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | awk -v threshold=$(COVERAGE_THRESHOLD) '/^total:/ {gsub("%", "", $$3); if ($$3 + 0 < threshold) {printf("coverage %.1f%% is below threshold %.1f%%\n", $$3 + 0, threshold); exit 1}}'

complexity:
	gocyclo -ignore "_test\\.go" -over $(COMPLEXITY_THRESHOLD) .

vuln:
	govulncheck ./...

lint:
	golangci-lint run ./...

audit: test race coverage complexity vuln lint

binary-test:
	bash ./test-binary.sh

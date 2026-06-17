COVERAGE_THRESHOLD ?= 99
COMPLEXITY_THRESHOLD ?= 6

.PHONY: test race coverage complexity vuln lint audit

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

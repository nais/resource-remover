test:
	go test -cover ./...

staticcheck:
	@echo "Running staticcheck..."
	go run honnef.co/go/tools/cmd/staticcheck@latest -f=stylish ./...

vulncheck:
	@echo "Running vulncheck..."
	go run golang.org/x/vuln/cmd/govulncheck@latest ./...

helm-lint:
	@echo "Running helm lint..."
	helm lint --strict ./charts

fmt:
	@echo "Running go fmt..."
	go fmt ./...

check: fmt staticcheck vulncheck helm-lint

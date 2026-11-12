.PHONY: test lint demo

test:
	test -z "$$(gofmt -l plugin tests examples)"
	go vet ./...
	cd examples/worker && go vet ./...
	cd tests/testdata/pflagmod && go vet ./...
	go test -race ./...

lint:
	claude plugin validate ./plugin --strict
	claude plugin validate . --strict

# Exit 1 means it found problems, which the broken examples have on purpose.
demo:
	go run ./plugin/scripts --pkg ./examples/api --container api,reindex examples/api/broken.yaml || test $$? -eq 1
	go run ./plugin/scripts --pkg ./examples/api --container api,reindex examples/api/fixed.yaml
	go run ./plugin/scripts --pkg examples/worker examples/worker/broken.yaml || test $$? -eq 1
	go run ./plugin/scripts --pkg examples/worker examples/worker/fixed.yaml

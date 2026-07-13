.PHONY: proto lint test

proto:
	$(MAKE) -C platform/api

lint:
	golangci-lint run ./...

test:
	go test ./...

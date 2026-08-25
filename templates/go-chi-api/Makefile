.PHONY: test lint run migrate

test:
	go test -race ./...

lint:
	go vet ./...

run:
	go run ./cmd/server

migrate:
	go run ./cmd/migrate

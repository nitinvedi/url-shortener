.PHONY: run test test-race test-integration fmt vet build compose-up compose-down

run:
	go run ./cmd/api

test:
	go test ./...

test-race:
	go test -race ./...

test-integration:
	TEST_DATABASE_URL="${TEST_DATABASE_URL}" go test ./... -count=1

fmt:
	gofmt -w $$(find . -name '*.go' -not -path './vendor/*')

vet:
	go vet ./...

build:
	go build -o bin/api ./cmd/api

compose-up:
	docker compose up --build

compose-down:
	docker compose down
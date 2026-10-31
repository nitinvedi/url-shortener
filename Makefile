.PHONY: run test test-race fmt vet build compose-up compose-down

run:
	go run ./cmd/api

test:
	go test ./...

test-race:
	go test -race ./...

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
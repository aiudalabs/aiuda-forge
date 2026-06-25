.PHONY: build test vet lint gate run-control run-worker tidy

build:
	go build ./...

test:
	go test ./...

vet:
	go vet ./...

lint: vet

gate:
	./.vibeforge-gate

tidy:
	go mod tidy

run-control:
	go run ./cmd/control

run-worker:
	go run ./cmd/worker

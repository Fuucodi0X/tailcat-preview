.PHONY: build docker test tidy

build:
	go build -o bin/gateway ./cmd/gateway
	go build -o bin/devpreview ./cmd/devpreview

docker:
	docker build -t tailcat-preview-gateway .

test:
	go test ./...

tidy:
	go mod tidy

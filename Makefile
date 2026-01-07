build:
	go build ./cmd/graphql-language-server

test:
	go test ./...

lint:
	bash lint.sh

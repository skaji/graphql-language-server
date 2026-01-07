build:
	go build ./cmd/graphql-language-server

test:
	go test ./...

lint:
	bash lint.sh

release-snapshot:
	goreleaser release --snapshot --clean

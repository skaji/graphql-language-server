build:
	go build ./cmd/graphql-language-server

test:
	go test ./...

lint:
	bash lint.sh

prettier-fix:
	prettier -w .

release-snapshot:
	goreleaser release --snapshot --clean

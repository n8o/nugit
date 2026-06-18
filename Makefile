.PHONY: build test vet fmt render install clean

build:
	go build -o nugit ./cmd/nugit

test:
	go test ./...

vet:
	go vet ./...
	@test -z "$$(gofmt -l .)" || (echo "gofmt needed:"; gofmt -l .; exit 1)

fmt:
	gofmt -w .

# Render the PR view for the current branch. Override BASE/HEAD as needed:
#   make render BASE=main HEAD=HEAD
BASE ?= main
HEAD ?= HEAD
render: build
	./nugit pr-render -base $(BASE) -head $(HEAD)

install:
	go install ./cmd/nugit

clean:
	rm -f nugit

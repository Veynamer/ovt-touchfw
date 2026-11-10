BINARY := touchfw

.PHONY: all build test fmt vet clean

all: build

build:
	go build -o $(BINARY) ./cmd/touchfw

test:
	go test ./...

fmt:
	gofmt -w .

vet:
	go vet ./...

clean:
	rm -f $(BINARY)

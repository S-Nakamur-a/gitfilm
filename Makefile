BINARY  := git-film
PKG     := ./cmd/git-film

.PHONY: build install test vet fmt fmt-check tidy clean help

help:
	@echo "Targets:"
	@echo "  build      - build ./$(BINARY)"
	@echo "  install    - install $(BINARY) into \$$GOBIN (so 'git film ...' works)"
	@echo "  test       - go test ./..."
	@echo "  vet        - go vet ./..."
	@echo "  fmt        - gofmt -w ."
	@echo "  fmt-check  - fail if any file needs gofmt"
	@echo "  tidy       - go mod tidy"
	@echo "  clean      - remove ./$(BINARY)"

build:
	go build -o $(BINARY) $(PKG)

install:
	go install $(PKG)

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

fmt-check:
	@out=$$(gofmt -l .); \
	if [ -n "$$out" ]; then \
		echo "gofmt needed:"; echo "$$out"; exit 1; \
	fi

tidy:
	go mod tidy

clean:
	rm -f $(BINARY)

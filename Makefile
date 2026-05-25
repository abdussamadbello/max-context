.PHONY: build test lint bench cross-compile clean install install-path

BINARY_NAME := max-context
GOFLAGS ?=
CGO_ENABLED ?= 1
PREFIX ?= $(HOME)/.local
BINDIR ?= $(PREFIX)/bin

# Windows: default install to %LOCALAPPDATA%\max-context\bin; Go builds .exe
ifeq ($(OS),Windows_NT)
  PREFIX ?= $(LOCALAPPDATA)/max-context
  BINDIR ?= $(PREFIX)/bin
  BINARY_OUT := bin/$(BINARY_NAME).exe
else
  BINARY_OUT := bin/$(BINARY_NAME)
endif

build:
	CGO_ENABLED=$(CGO_ENABLED) go build $(GOFLAGS) -o $(BINARY_OUT) ./cmd/max-context

# Install binary to BINDIR. Run: make install
install: build
	@mkdir -p $(BINDIR)
	@cp $(BINARY_OUT) $(BINDIR)/$(BINARY_NAME)$(if $(filter Windows_NT,$(OS)),.exe,)
	@echo "Installed to $(BINDIR)/$(BINARY_NAME)$(if $(filter Windows_NT,$(OS)),.exe,)"
	@echo "Ensure $(BINDIR) is in your PATH."
	$(if $(filter Windows_NT,$(OS)),@echo. && @echo "Windows: add to PATH via Settings > System > About > Advanced > Environment Variables > User PATH",)

# Install and add BINDIR to PATH (Unix: shell rc; Windows: instructions only).
install-path: install
	@if [ "$(OS)" = "Windows_NT" ]; then \
		echo "To add to PATH permanently on Windows:"; \
		echo "  1. Win+R, run: sysdm.cpl"; \
		echo "  2. Advanced tab > Environment Variables > User variables > Path > Edit > New"; \
		echo "  3. Add: $(BINDIR)"; \
		echo "Or in PowerShell (current user):"; \
		echo '  [Environment]::SetEnvironmentVariable("Path", "$(BINDIR);" + [Environment]::GetEnvironmentVariable("Path","User"), "User")'; \
	else \
		BINDIR="$(BINDIR)"; \
		EXPANDED=$$(echo $$BINDIR); \
		if echo ":$$PATH:" | grep -q ":$$EXPANDED:"; then \
			echo "$$EXPANDED is already on your PATH."; \
		else \
			RC=; \
			if [ -f "$$HOME/.zshrc" ]; then RC="$$HOME/.zshrc"; \
			elif [ -f "$$HOME/.bashrc" ]; then RC="$$HOME/.bashrc"; \
			elif [ -f "$$HOME/.profile" ]; then RC="$$HOME/.profile"; fi; \
			if [ -n "$$RC" ]; then \
				LINE="export PATH=\"$$EXPANDED:\$$PATH\""; \
				grep -qF "$$EXPANDED" "$$RC" 2>/dev/null || echo "$$LINE" >> "$$RC"; \
				echo "Added $$EXPANDED to PATH in $$RC. Run: source $$RC"; \
			else \
				echo "Add to your shell config: export PATH=\"$$EXPANDED:\$$PATH\""; \
			fi; \
		fi; \
	fi

test:
	go test $(GOFLAGS) ./...

test-race:
	go test -race $(GOFLAGS) ./...

lint:
	go vet ./...
	@which golangci-lint >/dev/null 2>&1 && golangci-lint run ./... || true

bench:
	go test -bench=. -benchmem $(GOFLAGS) ./...

cross-compile:
	@mkdir -p dist
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=1 go build -o dist/$(BINARY_NAME)-darwin-arm64 ./cmd/max-context
	GOOS=darwin GOARCH=amd64 CGO_ENABLED=1 go build -o dist/$(BINARY_NAME)-darwin-amd64 ./cmd/max-context
	GOOS=linux GOARCH=amd64 CGO_ENABLED=1 go build -o dist/$(BINARY_NAME)-linux-amd64 ./cmd/max-context
	GOOS=linux GOARCH=arm64 CGO_ENABLED=1 go build -o dist/$(BINARY_NAME)-linux-arm64 ./cmd/max-context
	GOOS=windows GOARCH=amd64 CGO_ENABLED=1 go build -o dist/$(BINARY_NAME)-windows-amd64.exe ./cmd/max-context
	GOOS=windows GOARCH=arm64 CGO_ENABLED=1 go build -o dist/$(BINARY_NAME)-windows-arm64.exe ./cmd/max-context

clean:
	rm -rf bin dist
	go clean -cache -testcache

install-tools:
	go install github.com/goreleaser/goreleaser@latest
	@which golangci-lint >/dev/null 2>&1 || go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest

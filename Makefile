BINARY     := ai-usage-exporter
MODULE     := github.com/danielrigobertojs/ai-usage-exporter
VERSION    := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT     := $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
BUILD_DATE := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -X '$(MODULE)/internal/version.Version=$(VERSION)' \
           -X '$(MODULE)/internal/version.Commit=$(COMMIT)' \
           -X '$(MODULE)/internal/version.BuildDate=$(BUILD_DATE)'

DIST := dist

PLATFORMS := darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64 windows/arm64

.PHONY: build
build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/$(BINARY)

.PHONY: cross
cross: $(PLATFORMS)

.PHONY: release-platforms
release-platforms:
	@printf '%s\n' $(PLATFORMS)

.PHONY: $(PLATFORMS)
$(PLATFORMS):
	$(eval OS := $(word 1,$(subst /, ,$@)))
	$(eval ARCH := $(word 2,$(subst /, ,$@)))
	$(eval EXT := $(if $(filter windows,$(OS)),.exe,))
	mkdir -p $(DIST)
	CGO_ENABLED=0 GOOS=$(OS) GOARCH=$(ARCH) go build -ldflags "$(LDFLAGS)" \
		-o $(DIST)/$(BINARY)-$(OS)-$(ARCH)$(EXT) ./cmd/$(BINARY)

.PHONY: test
test:
	go test ./... -race

.PHONY: vet
vet:
	go vet ./...

.PHONY: fmt
fmt:
	gofmt -l .

.PHONY: clean
clean:
	rm -rf $(DIST) $(BINARY)

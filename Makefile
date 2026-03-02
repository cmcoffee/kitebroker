APPNAME   := kitebroker
VERSION   := $(shell cat version.txt)
BUILDTIME := $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')

# Default output directory
OUTDIR    := build

# Go build flags
GOFLAGS   := -trimpath
LDFLAGS   := -s -w

.PHONY: all build clean linux windows darwin cross

# Default: build for the current platform
all: build

build:
	@mkdir -p $(OUTDIR)
	go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(OUTDIR)/$(APPNAME) .
	@echo "Built $(OUTDIR)/$(APPNAME) ($(VERSION))"

# Individual platform targets
linux:
	@mkdir -p $(OUTDIR)
	GOOS=linux GOARCH=amd64 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(OUTDIR)/$(APPNAME)_linux_amd64 .
	@echo "Built $(OUTDIR)/$(APPNAME)_linux_amd64"

windows:
	@mkdir -p $(OUTDIR)
	GOOS=windows GOARCH=amd64 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(OUTDIR)/$(APPNAME)_windows_amd64.exe .
	@echo "Built $(OUTDIR)/$(APPNAME)_windows_amd64.exe"

darwin:
	@mkdir -p $(OUTDIR)
	GOOS=darwin GOARCH=amd64 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(OUTDIR)/$(APPNAME)_darwin_amd64 .
	GOOS=darwin GOARCH=arm64 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(OUTDIR)/$(APPNAME)_darwin_arm64 .
	@echo "Built $(OUTDIR)/$(APPNAME)_darwin_amd64"
	@echo "Built $(OUTDIR)/$(APPNAME)_darwin_arm64"

# Build for all platforms
cross: linux windows darwin

clean:
	rm -rf $(OUTDIR)

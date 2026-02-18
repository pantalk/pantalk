VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  = -s -w -X github.com/chatbotkit/pantalk/internal/version.Version=$(VERSION)

CMDS = pantalk pantalkd pantalkctl pantalk-discord pantalk-slack pantalk-telegram pantalk-mattermost

.PHONY: all build clean test vet lint

all: build

build:
	@for cmd in $(CMDS); do \
		echo "Building $$cmd ($(VERSION))..."; \
		CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $$cmd ./cmd/$$cmd; \
	done

test:
	go test ./... -count=1

vet:
	go vet ./...

lint: vet
	@echo "lint ok"

clean:
	rm -f $(CMDS)

# Cross-compile a specific platform: make cross GOOS=darwin GOARCH=arm64
cross:
	@for cmd in $(CMDS); do \
		echo "Building $$cmd ($(VERSION)) for $(GOOS)/$(GOARCH)..."; \
		CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) go build -trimpath -ldflags "$(LDFLAGS)" -o $$cmd ./cmd/$$cmd; \
	done

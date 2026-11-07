INSTALL_DIR := $(HOME)/.local/bin
BINARY := ktree

.PHONY: install build clean

install: build
	cp bin/$(BINARY) $(INSTALL_DIR)/$(BINARY)

build:
	mkdir -p bin
	go build -o bin/$(BINARY) ./cmd/$(BINARY)

clean:
	rm -rf bin

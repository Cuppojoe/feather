TOOLCHAIN_BIN := $(HOME)/Development/controlplane/toolchain/.bin
BINARY := feather

.PHONY: build install clean

# Requires libx11-dev at build time (e.g. `apt install libx11-dev`). The
# resulting binary loads libX11.so.6 via dlopen at runtime.
build:
	go build -o $(BINARY) ./cmd/feather

install: build
	mkdir -p $(TOOLCHAIN_BIN)
	cp $(BINARY) $(TOOLCHAIN_BIN)/$(BINARY)

clean:
	rm -f $(BINARY)

BUILDDIR ?= .build
PREFIX ?= /usr
INSTALLBINDIR := $(DESTDIR)/$(PREFIX)/bin
VERSION ?= $(shell git describe --tags --dirty --always --match 'v*.*.*' --long)

build: $(BUILDDIR)
	@go build -v -o "$(BUILDDIR)" -trimpath -ldflags='-X main.Version=$(VERSION)'

install: build $(INSTALLBINDIR)
	@for bin in "$(BUILDDIR)"/*; do \
		cp "$${bin}" "$(INSTALLBINDIR)/$$(basename "$${bin}")"; \
		chmod 755 "$(INSTALLBINDIR)/$$(basename "$${bin}")"; \
	done

$(BUILDDIR) $(INSTALLBINDIR):
	@mkdir -p "$@"

docker:
	@docker build --build-arg version="$(VERSION)" .

clean:
	@rm -rf "$(BUILDDIR)"

.PHONY: build install docker clean

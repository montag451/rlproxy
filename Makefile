BUILDDIR ?= .build
PREFIX ?= /usr
INSTALLBINDIR := $(DESTDIR)/$(PREFIX)/bin
VERSION ?= $(shell git describe --tags --dirty --always --match 'v*.*.*')
DOCKER_IMAGE_NAME ?= mtag451/rlproxy
DOCKER_IMAGE_TAG := $(DOCKER_IMAGE_NAME):$(VERSION)

build: $(BUILDDIR)
	@go build -v -o "$(BUILDDIR)" -trimpath -ldflags='-X main.Version=$(VERSION)'

install: build $(INSTALLBINDIR)
	@for bin in "$(BUILDDIR)"/*; do \
		cp "$${bin}" "$(INSTALLBINDIR)/$$(basename "$${bin}")"; \
		chmod 755 "$(INSTALLBINDIR)/$$(basename "$${bin}")"; \
	done

$(BUILDDIR) $(INSTALLBINDIR):
	@mkdir -p "$@"

dockerbuild:
	@docker build --tag "$(DOCKER_IMAGE_TAG)" --build-arg version="$(VERSION)" .

dockerpush: dockerbuild
	@docker push "$(DOCKER_IMAGE_TAG)"

clean:
	@rm -rf "$(BUILDDIR)"

.PHONY: build install docker clean

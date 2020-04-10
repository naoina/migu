BIN_NAME="$(notdir $(PWD))"
BUILDFLAGS := -tags netgo -installsuffix netgo -ldflags '-w -s --extldflags "-static"'
GO_VERSION := 1.10
GO_PACKAGE := "$(shell go list)"
DB_IMAGE := mariadb:10.1.33
DB_HOST := migu-test-db-$(subst :,-,$(DB_IMAGE))
DOCKER_NETWORK := migu-test-net-$(subst :,-,$(DB_IMAGE))
DATADIR := /tmp/$(DB_HOST)

.PHONY: all
all: deps
	cd cmd/migu && CGO_ENABLED=0 go build -o $(BIN_NAME) $(BUILDFLAGS)

.PHONY: deps
deps:
	go mod download

.PHONY: test
test:
	go test ./...

.PHONY: test-all
test-all: deps
	$(MAKE) test

.PHONY: db
db:
	docker network inspect -f '{{.Name}}: {{.Id}}' $(DOCKER_NETWORK) || docker network create $(DOCKER_NETWORK)
	docker inspect -f='{{.Name}}: {{.Id}}' $(DB_HOST) || \
		docker run \
			--name=$(DB_HOST) \
			-v $(DATADIR):/var/lib/mysql \
			-e MYSQL_ALLOW_EMPTY_PASSWORD=1 \
			-e MYSQL_DATABASE=migu_test \
			-d --rm --net=$(DOCKER_NETWORK) \
			$(DB_IMAGE)

.PHONY: test-on-docker
define DOCKERFILE
FROM golang:latest
ENV GOROOT_FINAL /usr/lib/go
RUN git clone --depth=1 https://go.googlesource.com/go $$GOROOT_FINAL \
	&& cd $$GOROOT_FINAL/src \
	&& ./make.bash
ENV PATH $$GOROOT_FINAL/bin:$$PATH
endef
export DOCKERFILE
test-on-docker: db
ifeq ($(GO_VERSION),master)
	echo "$$DOCKERFILE" | docker build --no-cache -t golang:master -
endif
	docker run \
		-v $(PWD):/go/src/$(GO_PACKAGE) \
		-w /go/src/$(GO_PACKAGE) \
		--rm --net=$(DOCKER_NETWORK) \
		golang:$(GO_VERSION) \
		make DB_HOST=$(DB_HOST) test-all

.PHONY: clean
clean:
	$(RM) -f $(BIN_NAME)
	-docker kill $(DB_HOST)
	-docker network rm $(DOCKER_NETWORK)

BIN_NAME="$(notdir $(PWD))"
BUILDFLAGS := -tags netgo -installsuffix netgo -ldflags '-w -s --extldflags "-static"'
GO_VERSION := 1.14
GO_PACKAGE := "$(shell go list)"
BUILD_TAG ?= mysql
DB_IMAGE := mariadb:10.1.33
# DB_IMAGE := gcr.io/cloud-spanner-emulator/emulator
DB_ID := $(subst /,-,$(subst :,-,$(DB_IMAGE)))
export DB_HOST := migu-test-db-$(DB_ID)
DB_NAME := migu_test
DOCKER_NETWORK := migu-test-net-$(DB_ID)
DATADIR := /tmp/$(DB_HOST)
export SPANNER_PROJECT_ID ?= dummy
export SPANNER_INSTANCE_ID ?= migu-test-instance
export SPANNER_DATABASE_ID ?= $(DB_NAME)

.PHONY: all
all: deps
	cd cmd/migu && CGO_ENABLED=0 go build -o $(BIN_NAME) $(BUILDFLAGS)

.PHONY: deps
deps:
	go mod download

.PHONY: test
test:
	go test -tags $(BUILD_TAG) ./...

.PHONY: test-all
test-all: deps
	@echo $(shell go version)
	$(MAKE) test

.PHONY: db
db:
ifneq ($(DOCKER_NETWORK),host)
	docker network inspect -f '{{.Name}}: {{.Id}}' $(DOCKER_NETWORK) || docker network create $(DOCKER_NETWORK)
endif
	docker container inspect -f='{{.Name}}: {{.Id}}' $(DB_HOST) || \
		docker run \
			--name=$(DB_HOST) \
			-v $(DATADIR):/var/lib/mysql \
			-e MYSQL_ALLOW_EMPTY_PASSWORD=1 \
			-e MYSQL_DATABASE=$(DB_NAME) \
			-d --rm --net=$(DOCKER_NETWORK) \
			$(DB_IMAGE)
ifneq (,$(findstring gcr.io/cloud-spanner-emulator/emulator,$(DB_IMAGE)))
	sleep 5
	docker run -d --rm --net=$(DOCKER_NETWORK) curlimages/curl \
		curl -s $(DB_HOST):9020/v1/projects/$(SPANNER_PROJECT_ID)/instances --data '{"instanceId":"'$(SPANNER_INSTANCE_ID)'"}'
	docker run -d --rm --net=$(DOCKER_NETWORK) curlimages/curl \
		curl -s $(DB_HOST):9020/v1/projects/${SPANNER_PROJECT_ID}/instances/${SPANNER_INSTANCE_ID}/databases --data '{"createStatement": "CREATE DATABASE `'$(SPANNER_DATABASE_ID)'`"}'
endif

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
		make DB_HOST=$(DB_HOST) BUILD_TAG=$(BUILD_TAG) test-all

.PHONY: clean
clean:
	$(RM) -f $(BIN_NAME)
	-docker kill $(DB_HOST)
ifneq ($(DOCKER_NETWORK),host)
	-docker network rm $(DOCKER_NETWORK)
endif

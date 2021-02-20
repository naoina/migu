BIN_NAME="$(notdir $(PWD))"
BUILDFLAGS := -tags netgo -installsuffix netgo -ldflags '-w -s --extldflags "-static"'
GO_VERSION := 1.14
GO_PACKAGE := "$(shell go list)"
export TARGET_DB ?= mariadb:10.1.33 spanner:latest
DB_NAME := migu_test
DOCKER_NETWORK := migu-test-net
export SPANNER_PROJECT_ID ?= dummy
export SPANNER_INSTANCE_ID ?= migu-test-instance
export SPANNER_DATABASE_ID ?= $(DB_NAME)
export MIGU_DB_MYSQL_HOST := migu-test-mysql
export MIGU_DB_SPANNER_HOST := migu-test-spanner
MIGU_DB_HOSTS := $(MIGU_DB_MYSQL_HOST) $(MIGU_DB_SPANNER_HOST)

target_dbs = $(foreach db,$(TARGET_DB),$(word 1,$(subst :, ,$(db))))

.PHONY: all
all: deps
	cd cmd/migu && CGO_ENABLED=0 go build -o $(BIN_NAME) $(BUILDFLAGS)

.PHONY: deps
deps:
	go mod download

.PHONY: test/mysql
test/mysql:
	go test -run TestMySQL ./...

.PHONY: test/mariadb
test/mariadb:
	go test -run TestMySQL ./...

.PHONY: test/spanner
test/spanner:
	go test -run TestSpanner ./...

.PHONY: test-all
test-all: deps
	@echo $(shell go version)
	$(MAKE) test

.PHONY: test
test: $(foreach db,$(target_dbs),test/$(db))

.PHONY: docker-network
docker-network:
ifneq ($(DOCKER_NETWORK),host)
	docker network inspect -f '{{.Name}}: {{.Id}}' $(DOCKER_NETWORK) || docker network create $(DOCKER_NETWORK)
endif

define DB_mysql_template
.PHONY: db/mysql
db/mysql: docker-network
	docker container inspect -f='{{.Name}}: {{.Id}}' $(MIGU_DB_MYSQL_HOST) || \
		docker run \
			--name=$(MIGU_DB_MYSQL_HOST) \
			-v /tmp/migu-test-db:/var/lib/mysql \
			-e MYSQL_ALLOW_EMPTY_PASSWORD=1 \
			-e MYSQL_DATABASE=$(DB_NAME) \
			-d --rm --net=$(DOCKER_NETWORK) \
			mysql:$(or $(1),latest)
endef

define DB_mariadb_template
.PHONY: db/mariadb
db/mariadb: docker-network
	docker container inspect -f='{{.Name}}: {{.Id}}' $(MIGU_DB_MYSQL_HOST) || \
		docker run \
			--name=$(MIGU_DB_MYSQL_HOST) \
			-v /tmp/migu-test-db:/var/lib/mysql \
			-e MYSQL_ALLOW_EMPTY_PASSWORD=1 \
			-e MYSQL_DATABASE=$(DB_NAME) \
			-d --rm --net=$(DOCKER_NETWORK) \
			mariadb:$(or $(1),latest)
endef

define DB_spanner_template
.PHONY: db/spanner
db/spanner: docker-network
	docker container inspect -f='{{.Name}}: {{.Id}}' $(MIGU_DB_SPANNER_HOST) || \
		docker run \
			--name=$(MIGU_DB_SPANNER_HOST) \
			-d --rm --net=$(DOCKER_NETWORK) \
			gcr.io/cloud-spanner-emulator/emulator:$(or $(1),latest)
	sleep 5
	docker run -d --rm --net=$(DOCKER_NETWORK) curlimages/curl \
		curl -s $(MIGU_DB_SPANNER_HOST):9020/v1/projects/$(SPANNER_PROJECT_ID)/instances --data '{"instanceId":"'$(SPANNER_INSTANCE_ID)'"}'
	docker run -d --rm --net=$(DOCKER_NETWORK) curlimages/curl \
		curl -s $(MIGU_DB_SPANNER_HOST):9020/v1/projects/${SPANNER_PROJECT_ID}/instances/${SPANNER_INSTANCE_ID}/databases --data '{"createStatement": "CREATE DATABASE `'$(SPANNER_DATABASE_ID)'`"}'
endef

$(foreach db,$(TARGET_DB),$(eval $(call DB_$(word 1,$(subst :, ,$(db)))_template,$(word 2,$(subst :, ,$(db))))))

.PHONY: db
db: $(foreach db,$(target_dbs),db/$(db))

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
		make TARGET_DB="$(TARGET_DB)" test-all

.PHONY: clean
clean:
	$(RM) -f $(BIN_NAME)
	-docker kill $(MIGU_DB_HOSTS)
ifneq ($(DOCKER_NETWORK),host)
	-docker network rm $(DOCKER_NETWORK)
endif

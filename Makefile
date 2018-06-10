BIN_NAME="$(notdir $(PWD))"
BUILDFLAGS := -tags netgo -installsuffix netgo -ldflags '-w -s --extldflags "-static"'
DATADIR := /tmp/migu-test-db
GO_VERSION := 1.10
GO_PACKAGE := "$(shell go list)"
DOCKER_NETWORK := migu-test-net
DB_HOST := migu-test-db
MARIADB_VERSION := 10.1.33

.PHONY: all
all: deps
	cd cmd/migu && go build -o $(BIN_NAME) $(BUILDFLAGS)

.PHONY: deps
deps:
	go get -v -u github.com/go-sql-driver/mysql
	go get -v ./...

.PHONY: test-deps
test-deps: deps
	go test -v -i ./...

.PHONY: test
test:
	go test ./...

.PHONY: test-all
test-all: test-deps
	$(MAKE) test

.PHONY: mariadb
mariadb:
	docker network inspect -f '{{.Name}}: {{.Id}}' $(DOCKER_NETWORK) || docker network create $(DOCKER_NETWORK)
	docker inspect -f='{{.Name}}: {{.Id}}' $(DB_HOST) || \
		docker run \
			--name=$(DB_HOST) \
			-v $(DATADIR):/var/lib/mysql \
			-e MYSQL_ALLOW_EMPTY_PASSWORD=1 \
			-e MYSQL_DATABASE=migu_test \
			-d --rm --net=$(DOCKER_NETWORK) \
			mariadb:$(MARIADB_VERSION)

.PHONY: test-on-docker
test-on-docker: mariadb
	docker run \
		-v $(PWD):/go/src/$(GO_PACKAGE) \
		-w /go/src/$(GO_PACKAGE) \
		--rm --net=$(DOCKER_NETWORK) \
		golang:$(GO_VERSION) \
		make test-all

.PHONY: clean
clean:
	$(RM) -f $(BIN_NAME)
	-docker kill $(DB_HOST)
	-docker network rm $(DOCKER_NETWORK)

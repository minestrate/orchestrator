# Variables
main_package_path = .
binary_name = main
docker_image = minestrate-app

# ==================================================================================== #
# HELPERS
# ==================================================================================== #

## help: print this help message
.PHONY: help
help:
	@echo 'Usage:'
	@sed -n 's/^##//p' ${MAKEFILE_LIST} | column -t -s ':' |  sed -e 's/^/ /'

.PHONY: no-dirty
no-dirty:
	@test -z "$(shell git status --porcelain)"

# ==================================================================================== #
# QUALITY CONTROL & LINTING
# ==================================================================================== #

## tidy: format code and sync dependencies
.PHONY: tidy
tidy:
	go fmt ./...
	go mod tidy -v

## lint: run golangci-lint
.PHONY: lint
lint:
	go run github.com/golangci/golangci-lint/cmd/golangci-lint@latest run ./...

## audit: run quality control checks (security, style, types)
.PHONY: audit
audit: test
	go mod verify
	go vet ./...
	go run honnef.co/go/tools/cmd/staticcheck@latest -checks=all,-ST1000,-U1000 ./...
	go run golang.org/x/vuln/cmd/govulncheck@latest ./...

# ==================================================================================== #
# TESTING
# ==================================================================================== #

## test: run all tests
.PHONY: test
test:
	go test -v -race -buildvcs ./...

## test/integration: run integration tests (requires Docker)
.PHONY: test/integration
test/integration:
	go test -tags=integration -v ./...

## test/cover: run all tests and display coverage in browser
.PHONY: test/cover
test/cover:
	go test -v -race -buildvcs -coverprofile=/tmp/coverage.out ./...
	go tool cover -html=/tmp/coverage.out

# ==================================================================================== #
# DEVELOPMENT & EXECUTION
# ==================================================================================== #

## build: build the application for current OS
.PHONY: build
build:
	go build -o=/tmp/bin/${binary_name} ${main_package_path}

## run: build and run the application
.PHONY: run
run: build
	/tmp/bin/${binary_name}

# ==================================================================================== #
# DOCKER
# ==================================================================================== #

## docker/build: build the docker image
.PHONY: docker/build
docker/build:
	docker build -t ${docker_image} .

## docker/run: run the application in a docker container
.PHONY: docker/run
docker/run:
	docker run --rm -it ${docker_image}

# ==================================================================================== #
# OPERATIONS
# ==================================================================================== #

## push: push changes to the remote Git repository
.PHONY: push
push: audit no-dirty
	git push

## production/deploy: build optimized binary and compress
.PHONY: production/deploy
production/deploy: audit no-dirty
	GOOS=linux GOARCH=amd64 go build -ldflags='-s -w' -o=/tmp/bin/linux_amd64/${binary_name} ${main_package_path}
	upx -5 /tmp/bin/linux_amd64/${binary_name}
# 构建产物
BINARY  := runner-manager
VERSION ?= dev

# 本地构建 Runner 镜像的默认 tag；使用 CI 推送的镜像时为同仓库名、tag 带 -runner，如 ghcr.io/<owner>/<repo>:main-runner
RUNNER_IMAGE ?= ghcr.io/soulteary/runner-fleet-runner:main

.PHONY: build build-agent build-all test run docker-build docker-build-runner docker-run docker-stop clean help

help:
	@echo "targets: build build-agent build-all test run docker-build docker-build-runner docker-run docker-stop clean"

build:
	go build -ldflags "-X main.Version=$(VERSION)" -o $(BINARY) ./cmd/runner-manager

build-agent:
	go build -o runner-agent ./cmd/runner-agent

build-all: build build-agent

test:
	go test ./...

run: build
	./$(BINARY)

docker-build:
	docker build --build-arg VERSION=$(VERSION) -t runner-manager:$(VERSION) .

docker-build-runner:
	docker build -f Dockerfile.runner -t $(RUNNER_IMAGE) .

docker-run: docker-stop
	docker run -d --name runner-manager -p 8080:8080 \
		-v $(PWD)/config.yaml:/app/config.yaml \
		-v $(PWD)/runners:/app/runners \
		runner-manager:$(VERSION)

docker-stop:
	-docker stop runner-manager 2>/dev/null; docker rm runner-manager 2>/dev/null; true

clean:
	rm -f $(BINARY) runner-agent

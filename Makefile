# 构建产物
BINARY  := runner-manager
VERSION ?= dev

# 本地构建 Runner 镜像的默认 tag；使用 CI 推送的镜像时为同仓库名、tag 带 -runner，如 ghcr.io/<owner>/<repo>:v1.0.0-runner
RUNNER_IMAGE ?= ghcr.io/soulteary/runner-fleet:v1.0.0-runner

# 镜像内 app 用户加入的 docker 组 GID，需与宿主机一致才能访问 docker.sock（getent group docker | cut -d: -f3）
DOCKER_GID ?= 999

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
	docker build --build-arg VERSION=$(VERSION) --build-arg DOCKER_GID=$(DOCKER_GID) -t runner-manager:$(VERSION) .

docker-build-runner:
	docker build -f Dockerfile.runner --build-arg DOCKER_GID=$(DOCKER_GID) -t $(RUNNER_IMAGE) .

# 需 Basic Auth 时请使用 docker compose（会读取 .env），或在本 target 的 docker run 中增加 -e BASIC_AUTH_PASSWORD=... -e BASIC_AUTH_USER=admin
docker-run: docker-stop
	docker run -d --name runner-manager -p 8080:8080 \
		-v $(PWD)/config:/app/config \
		-v $(PWD)/runners:/app/runners \
		runner-manager:$(VERSION)

docker-stop:
	-docker stop runner-manager 2>/dev/null; docker rm runner-manager 2>/dev/null; true

clean:
	rm -f $(BINARY) runner-agent

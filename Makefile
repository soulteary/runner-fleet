# 构建产物
BINARY  := runner-manager
VERSION ?= dev

# 本地构建 Runner 镜像的默认 tag；使用 CI 推送的镜像时为同仓库名、tag 带 -runner，如 ghcr.io/<owner>/<repo>:v1.7.0-runner
RUNNER_IMAGE ?= ghcr.io/soulteary/runner-fleet:v1.7.0-runner

# 镜像内 app 用户加入的 docker 组 GID，需与宿主机一致才能访问 docker.sock（getent group docker | cut -d: -f3）
DOCKER_GID ?= 999

# 自定义 Runner 镜像示例（examples/runner-images/Dockerfile.$(EXAMPLE)）
EXAMPLE ?= android
IMAGE   ?= runner-fleet-$(EXAMPLE)-runner:dev

.PHONY: build build-agent build-all test test-race lint check install-ci-recipes run docker-build docker-build-runner docker-build-runner-example docker-run docker-stop clean help

help:
	@echo "targets: build build-agent build-all test test-race lint check install-ci-recipes run docker-build docker-build-runner docker-build-runner-example docker-run docker-stop clean"
	@echo "  check: 提交前跑这一个——CI 会跑的检查都在里面"
	@echo "  install-ci-recipes: 装 check 需要的 ci-recipes（版本跟 CI 钉的同一个）"
	@echo "  docker-build-runner-example: 构建自定义 Runner 镜像示例，如"
	@echo "    make docker-build-runner-example EXAMPLE=android IMAGE=your-registry/android-runner:1"

COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS = -X main.Version=$(VERSION) -X main.Commit=$(COMMIT) -X main.BuildDate=$(BUILD_DATE)

build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/runner-manager

build-agent:
	go build -o runner-agent ./cmd/runner-agent

build-all: build build-agent

test:
	go test ./...

# CI 跑的是这个。并发面（注册队列、runnerOps 锁、Agent 令牌的单一胜者创建）
# 出问题时普通 go test 是静默通过的，提交前至少跑一次。
test-race:
	go test -race ./...

# 与 CI 里 Test job 的 Lint 那一步对应。范围取 ./...，是两个 workflow
# （./cmd/runner-manager/... ./internal/... 与 ./cmd/runner-agent/... ./internal/...）
# 的并集再大一点，本地过了 CI 必过。
#
# 不钉版本：CI 用的是 version: latest，这边钉死反而会两头判得不一样。
GOLANGCI_LINT ?= golangci-lint

lint:
	@command -v $(GOLANGCI_LINT) >/dev/null 2>&1 || { \
		echo "未找到 $(GOLANGCI_LINT)。安装："; \
		echo "  go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest"; \
		echo '装好后确认 $$(go env GOPATH)/bin 在 PATH 上。'; \
		exit 1; \
	}
	$(GOLANGCI_LINT) run --max-same-issues=100000 ./...

# 版本号一致性与译文章节结构这两个检查，由 soulteary/ci-recipes 的 runner-fleet recipe
# 提供，本仓库只出 scripts/ci-recipes.conf。
#
# 版本不在这里重写一遍，而是从 workflow 里读：两处各钉一个版本、改一处忘另一处，就是
# 本地与 CI 判得不一样，而那正是 lint 当初留下的口子。读不出来时 check 直接失败，
# 不拿空版本往下走。
CI_RECIPES          ?= ci-recipes
CI_RECIPES_WORKFLOW := .github/workflows/ci-consistency.yml
CI_RECIPES_VERSION  ?= $(shell sed -n 's/^[[:space:]]*CI_RECIPES_VERSION:[[:space:]]*"\([^"]*\)".*/\1/p' $(CI_RECIPES_WORKFLOW))

# 提交前跑这一个。CI 会跑的检查都收在这里，不必每次凭记忆拼清单——
# 漏掉的那一项永远是本地绿、CI 红的那一项（上一次漏的正是 lint）。
check:
	@out=$$(gofmt -l ./cmd ./internal); \
	if [ -n "$$out" ]; then \
		echo "以下文件未格式化，请运行 gofmt -w ./cmd ./internal:"; \
		echo "$$out"; \
		exit 1; \
	fi
	go vet ./...
	$(MAKE) lint
	go test -race ./...
	@command -v $(CI_RECIPES) >/dev/null 2>&1 || { \
		echo "未找到 $(CI_RECIPES)。安装：make install-ci-recipes"; \
		echo '装好后确认 $$(go env GOPATH)/bin 在 PATH 上。'; \
		exit 1; \
	}
	$(CI_RECIPES) runner-fleet check-version-consistency
	$(CI_RECIPES) runner-fleet check-docs-structure
	@echo "全部检查通过"

run: build
	./$(BINARY)

docker-build:
	docker build --build-arg VERSION=$(VERSION) --build-arg DOCKER_GID=$(DOCKER_GID) -t runner-manager:$(VERSION) .

docker-build-runner:
	docker build -f Dockerfile.runner --build-arg DOCKER_GID=$(DOCKER_GID) -t $(RUNNER_IMAGE) .

# 在本仓库 Runner 镜像之上叠加工具链，详见 examples/runner-images/README.md
docker-build-runner-example:
	docker build -f examples/runner-images/Dockerfile.$(EXAMPLE) -t $(IMAGE) .

# 需 Basic Auth 时请使用 docker compose（会读取 .env），或在本 target 的 docker run 中增加 -e BASIC_AUTH_PASSWORD=... -e BASIC_AUTH_USER=admin
docker-run: docker-stop
	docker run -d --name runner-manager -p 8080:8080 \
		-v $(PWD)/config:/app/config \
		-v $(PWD)/runners:/app/runners \
		runner-manager:$(VERSION)

docker-stop:
	-docker stop runner-manager 2>/dev/null; docker rm runner-manager 2>/dev/null; true

# 装 check 需要的 ci-recipes。版本从 workflow 读，所以这里、文档里、CI 里都不必各写一遍
# 版本号——写了就会各自过期，而且版本号一致性检查本身也会把那个 vX.Y.Z 当成本仓库的
# 版本号引用报出来（它只认字面量，分不清那是别人的模块版本）。
install-ci-recipes:
	@[ -n "$(CI_RECIPES_VERSION)" ] || { \
		echo "无法从 $(CI_RECIPES_WORKFLOW) 读出 CI_RECIPES_VERSION，不能用空版本安装。"; \
		exit 1; \
	}
	go install "github.com/soulteary/ci-recipes/cmd/ci-recipes@$(CI_RECIPES_VERSION)"
	@echo "装好了。确认 $$(go env GOPATH)/bin 在 PATH 上。"

clean:
	rm -f $(BINARY) runner-agent

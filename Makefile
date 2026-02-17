# 构建产物
BINARY  := runner-manager
VERSION ?= dev

.PHONY: build test run docker-build docker-run docker-stop clean help

help:
	@echo "targets: build test run docker-build docker-run docker-stop clean"

build:
	go build -ldflags "-X main.Version=$(VERSION)" -o $(BINARY) .

test:
	go test ./...

run: build
	./$(BINARY)

docker-build:
	docker build --build-arg VERSION=$(VERSION) -t runner-manager:$(VERSION) .

docker-run: docker-stop
	docker run -d --name runner-manager -p 8080:8080 \
		-v $(PWD)/config.yaml:/app/config.yaml \
		-v $(PWD)/runners:/app/runners \
		runner-manager:$(VERSION)

docker-stop:
	-docker stop runner-manager 2>/dev/null; docker rm runner-manager 2>/dev/null; true

clean:
	rm -f $(BINARY)

# Unix Makefile — furry-drama-be-neo 后端（GNU make）
# 常用：make build / make run / make test / make vet / make clean
# Windows 请使用：nmake /f Makefile.win

BINARY ?= bin/furry-drama-be-neo
PKG    ?= ./cmd/server
SWAG   ?= swag

.PHONY: all build run seed test vet fmt docs clean

all: build

build:
	go build -o $(BINARY) $(PKG)

run:
	go run $(PKG)

seed:
	go run ./cmd/seed-admin

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -l -w .

# 重新生成 Swagger 产物（需要 swag，安装：go install github.com/swaggo/swag/cmd/swag@latest）
docs:
	$(SWAG) init -g cmd/server/main.go --output docs

clean:
	rm -f $(BINARY)

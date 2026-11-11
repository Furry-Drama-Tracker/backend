# Unix Makefile — furry-drama-be-neo 后端（GNU make）
# 常用：make build / make release / make run / make test / make vet / make clean
# Windows 请使用：nmake /f Makefile.win
# 交叉编译：make release GOOS=linux GOARCH=arm64

BIN_DIR ?= bin
NAME    ?= furry-drama-be-neo
PKG     ?= ./cmd/server
SWAG    ?= swag

# 目标平台：留空表示交给 Go 自己决定（即宿主平台）。GOOS/GOARCH 通过环境变量
# 传给 go 命令，所以 make build GOOS=linux GOARCH=arm64 就能交叉编译。
GOOS   ?=
GOARCH ?=
export GOOS GOARCH

# 输出文件名：Windows 目标带 .exe，其余平台不带
TARGET_GOOS = $(if $(GOOS),$(GOOS),$(shell go env GOOS))
BINARY ?= $(BIN_DIR)/$(NAME)$(if $(filter windows,$(TARGET_GOOS)),.exe)

# 发布构建的链接参数：-s 去掉符号表，-w 去掉 DWARF 调试信息
STRIP_LDFLAGS ?= -s -w

.PHONY: all build release run seed test vet fmt docs diag clean

all: build

build:
	go build -o $(BINARY) $(PKG)

# 发布构建：-trimpath 去掉本机源码路径，再剥离符号表与调试信息
release:
	go build -trimpath -ldflags "$(STRIP_LDFLAGS)" -o $(BINARY) $(PKG)

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

# 认证排障（只读）：按邮箱或 _id 输出会话墓碑 + 认证事件时间线
# 用法：make diag ARGS="-user=someone@example.com -days=7"
#       make diag ARGS="-recent -hours=6"
diag:
	go run ./cmd/diag $(ARGS)

clean:
	rm -f $(BINARY)

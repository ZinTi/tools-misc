# filebox/Makefile
# 项目根目录下的 Makefile，用于构建、运行和清理

.PHONY: all build run clean deps

# 默认目标
all: build

# 安装依赖（进入 src 目录）
deps:
	cd src && go mod tidy

# 构建二进制文件，输出到项目根目录的 bin/ 下
build: deps
	mkdir -p bin
	cd src && go build -o ../bin/filebox .

# 直接运行（开发模式，不生成二进制）
run:
	cd src && go run main.go

# 清理编译产物
clean:
	rm -rf bin
	rm -f src/filebox

# 帮助信息
help:
	@echo "可用目标:"
	@echo "  deps    - 下载并整理 Go 依赖"
	@echo "  build   - 编译可执行文件到 bin/filebox"
	@echo "  run     - 直接运行 (go run)"
	@echo "  clean   - 删除编译产物"
	@echo "  help    - 显示此帮助"

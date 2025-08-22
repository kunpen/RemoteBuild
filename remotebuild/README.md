3️⃣ main.gomain.go

把我前面给你的完整 remote_build.go 代码直接复制过来。

4️⃣ README.md（使用说明）
# Remote Build Tool

## 功能
- 完全远程 CMake 构建
- 实时输出远程构建日志
- 可选只下载最终可执行文件/库
- 支持 JSON/YAML 配置文件
- 命令行参数优先于配置文件

## 编译
```bash
go build -o remote_build main.go

使用
# 默认使用 config.json
./remote_build --src ./my_cmake_project --remote-dir /tmp/remote_build

# 覆盖配置文件里的 host
./remote_build --host 192.168.1.101 --src ./my_cmake_project --remote-dir /tmp/remote_build

# 只下载最终可执行文件
./remote_build --src ./my_cmake_project --remote-dir /tmp/remote_build --artifacts bin/myapp,lib/mylib.so

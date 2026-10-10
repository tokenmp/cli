# tokenmp

TokenMP 的命令行工具。当前只提供帮助与版本查询，业务命令后续再加。

命令树和帮助页面基于开源项目搭建，没有自己写参数解析。

| 依赖 | 用途 |
| --- | --- |
| [spf13/cobra](https://github.com/spf13/cobra) | 命令树、子命令、`help` 子命令与 `-h` 标志 |
| [charmbracelet/fang](https://github.com/charmbracelet/fang) | 接管帮助与错误渲染，提供 `--version` 与 `-v` |
| [charmbracelet/lipgloss](https://charm.land/lipgloss) | `version` 子命令的着色 |
| [charmbracelet/colorprofile](https://github.com/charmbracelet/colorprofile) | 按终端能力降级颜色 |

## 命令

| 命令 | 输出 |
| --- | --- |
| `tokenmp help` | 完整帮助。`tokenmp help version` 看单个命令 |
| `tokenmp --help`、`tokenmp -h` | 完整帮助 |
| `tokenmp --version`、`tokenmp -v` | 单行版本，例如 `tokenmp version dev (8c6f342)` |
| `tokenmp version` | 版本表，见下 |
| `tokenmp` | 不带参数时打印帮助 |

`tokenmp version` 的字段：

```
tokenmp dev

commit:   8c6f342976c515d563110fbb2555c3cec428e4da
built:    2026-10-10T06:59:29Z
go:       go1.27.0
platform: linux/amd64
```

版本取自链接器注入的变量，没有注入时回退到 Go 工具链嵌进二进制的信息，
字段为空的那些行不打印。未知命令会以状态码 1 退出，错误信息由 fang 上色。

## 构建

```
make build     # 产物在 bin/tokenmp
make install   # 安装到 $GOBIN
make test      # go test ./...
make lint      # go vet，装有 golangci-lint 时一起跑
make fmt
make tidy
make clean
```

`make build` 从 git 标签取版本号：HEAD 上有标签时写成 `v0.1.0-3-gabc1234`，
没有标签就写 `dev`。提交号取自 `git rev-parse HEAD`，构建时间取自当前 UTC 时间，
两者和版本号一起用 `-ldflags -X` 注入 [版本元数据](internal/version/version.go)。

直接跑 `go build` 也能得到可用的二进制，只是版本回退到 Go 构建信息，
没有标签时显示 `(devel)` 或 `unknown (built from source)`。

## 目录

| 路径 | 内容 |
| --- | --- |
| [main.go](main.go) | 入口，调用 `cmd.Execute` 并按返回值设置退出码 |
| [cmd/root.go](cmd/root.go) | 根命令，以及 fang 的接线 |
| [cmd/version.go](cmd/version.go) | `version` 子命令与着色 |
| [cmd/root_test.go](cmd/root_test.go) | 四个命令入口的测试 |
| [internal/version/version.go](internal/version/version.go) | 构建元数据与回退逻辑 |

帮助里只出现 `help` 和 `version` 两个命令。cobra 自带的 `completion` 与
fang 自带的隐藏 `man` 命令在 [cmd/root.go](cmd/root.go) 里用
`fang.WithoutCompletions()`、`fang.WithoutManpage()` 关掉了，删掉这两行即可恢复。

## 本机注意

这台机器连不上 `proxy.golang.org`，取模块时会一直卡在建立连接上。
已在 `~/.config/go/env` 写入下面两项，构建与测试都能正常联网：

```
GOPROXY=https://goproxy.cn,direct
GOSUMDB=sum.golang.google.cn
```

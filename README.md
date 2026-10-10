# tokenmp

TokenMP 的命令行工具。目前提供版本查询与本地插件管理两组命令。

命令树、帮助页面与插件宿主都来自开源项目，没有自己写参数解析与插件运行时。

| 依赖 | 用途 |
| --- | --- |
| [spf13/cobra](https://github.com/spf13/cobra) | 命令树、子命令、`help` 子命令与 `-h` 标志 |
| [charmbracelet/fang](https://github.com/charmbracelet/fang) | 帮助与错误渲染，提供 `--version` 与 `-v` |
| [charmbracelet/lipgloss](https://charm.land/lipgloss) | 表格与字段的着色 |
| [charmbracelet/colorprofile](https://github.com/charmbracelet/colorprofile) | 按终端能力降级颜色 |
| [tokenmp/core](https://github.com/tokenmp/core) | 插件宿主、配置模型与插件接口定义 |

## 命令

| 命令 | 输出 | 示例 |
| --- | --- | --- |
| `tokenmp help` | 完整帮助。`tokenmp help plugin` 看命令组的说明 | `tokenmp help plugin check` |
| `tokenmp --help`、`tokenmp -h` | 完整帮助 | `tokenmp plugin --help` |
| `tokenmp --version`、`tokenmp -v` | 单行版本，例如 `tokenmp version dev (8c6f342)` | `tokenmp -v` |
| `tokenmp version` | 版本表，字段为 commit、built、go、platform | `tokenmp version` |
| `tokenmp plugin <子命令>` | 本地插件管理，见下 | `tokenmp plugin list` |
| `tokenmp` | 不带参数时打印帮助 | `tokenmp` |

命令失败时以状态码 1 退出，错误信息由 fang 上色。中文错误文案不带句点，
也不把首词大写，这两处与 fang 的默认处理器不同。

## 插件管理

插件是单文件同步 ECMAScript 模块，放在配置 `plugins.dir` 指向的目录里。是否参与
转发与执行顺序记在同一份配置的 `plugins.list` 段里。`plugin` 命令只读写配置与插件
文件，不接触正在运行的服务，改动在服务重启或重载配置后生效。

| 子命令 | 作用 | 示例 |
| --- | --- | --- |
| `plugin list`、`plugin ls` | 列出插件与启用状态，顺序即执行顺序 | `tokenmp plugin list --json` |
| `plugin show <key>`、`plugin info` | 自述信息、状态、限额与 `config` 段 | `tokenmp plugin show echo` |
| `plugin check [key...]` | 检查语法、自述信息与配置登记，有致命问题时退出码 1 | `tokenmp plugin check echo html-escape` |
| `plugin replay <key>` | 用 fixture 回放插件，有失败用例时退出码 1 | `tokenmp plugin replay echo` |
| `plugin enable <key...>` | 把 `enabled` 设为 true，条目不存在时补一条 | `tokenmp plugin enable echo html-escape` |
| `plugin disable <key...>` | 把 `enabled` 设为 false | `tokenmp plugin disable echo` |
| `plugin order <key> <number>` | 设置执行顺序，越小越先执行 | `tokenmp plugin order echo 10` |
| `plugin hooks` | 列出四个钩子的时机与宿主限额 | `tokenmp plugin hooks --json` |
| `plugin new <key>` | 生成插件骨架与 fixture 骨架 | `tokenmp plugin new html-escape` |

每个子命令的帮助里都有 `EXAMPLES` 段，`tokenmp help plugin <子命令>` 可以直接看：

```
$ tokenmp help plugin replay
  EXAMPLES

    # 用插件旁边的 fixture
    tokenmp plugin replay echo

    # 指定另一份 fixture
    tokenmp plugin replay echo --fixture plugins/echo/fixture.json

    # 换一份配置，插件目录跟着配置走
    tokenmp plugin replay echo --config /etc/tokenmp/config.yaml
```

三个标志对整组生效：

| 标志 | 默认值 | 含义 |
| --- | --- | --- |
| `--config` | `config.yaml` | 配置文件路径，插件目录的相对路径以它所在目录为基准 |
| `--dir` | 取配置里的 `plugins.dir` | 直接指定插件目录，配置文件缺失时用得上 |
| `--json` | 关 | 机器可读输出，`list`、`show`、`check`、`replay`、`hooks` 都支持 |

```
tokenmp plugin list
tokenmp plugin show echo
tokenmp plugin check --json
tokenmp plugin check echo html-escape
tokenmp plugin replay echo --fixture plugins/echo/fixture.json
tokenmp plugin enable echo html-escape
tokenmp plugin disable echo
tokenmp plugin order echo 10
tokenmp plugin hooks --json
tokenmp plugin new html-escape --name "HTML 转义"
```

`list` 把插件目录与配置合起来看，因此四种状态都会出现：启用、停用、未登记、
编译失败。单个插件编译失败不影响这份列表，原因单独列在表后。配置里登记了但目录里
没有的插件也列出来。

`check` 查三件事：源码能否编译并导出 `meta` 与钩子，源码里是否用到了宿主不提供的
能力，以及配置与目录是否一致。语法错误、`meta.key` 与目录名不符、登记了却没有文件
算致命问题，未登记算提示。

`enable`、`disable`、`order` 会改写配置文件：注释与缩进保留，空行可能被去掉。
写回后重新加载校验，配置不合法就还原原文件并报错。`enable` 与 `order` 先确认插件
目录里有这个 key，不会把不存在的插件写进配置。

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

直接跑 `go build` 也能得到可用的二进制，只是版本回退到 Go 构建信息。

## 目录

| 路径 | 内容 |
| --- | --- |
| [main.go](main.go) | 入口，调用 `cmd.Execute` 并按返回值设置退出码 |
| [cmd/root.go](cmd/root.go) | 根命令，以及 fang 的接线 |
| [cmd/version.go](cmd/version.go) | `version` 子命令 |
| [cmd/plugin.go](cmd/plugin.go) | `plugin` 命令组与共用标志 |
| [cmd/plugin_list.go](cmd/plugin_list.go) | 列出插件与状态 |
| [cmd/plugin_show.go](cmd/plugin_show.go) | 单个插件的详情 |
| [cmd/plugin_check.go](cmd/plugin_check.go) | 静态检查与配置登记比对 |
| [cmd/plugin_replay.go](cmd/plugin_replay.go) | fixture 回放 |
| [cmd/plugin_state.go](cmd/plugin_state.go) | 启用、停用与顺序 |
| [cmd/plugin_hooks.go](cmd/plugin_hooks.go) | 钩子与限额说明 |
| [cmd/plugin_new.go](cmd/plugin_new.go) | 生成插件骨架 |
| [cmd/style.go](cmd/style.go) | 配色、表格与字段排版 |
| [cmd/errors.go](cmd/errors.go) | 错误渲染，去掉了英文句点与首词大写 |
| [internal/pluginmgr](internal/pluginmgr/env.go) | 配置、插件目录与宿主的装配 |
| [internal/configedit](internal/configedit/configedit.go) | 保留注释的配置改写 |
| [internal/version](internal/version/version.go) | 构建元数据与回退逻辑 |

帮助里只出现 `help`、`version` 与 `plugin`。cobra 自带的 `completion` 与 fang
自带的隐藏 `man` 命令在 [cmd/root.go](cmd/root.go) 里关掉了，去掉
`fang.WithoutCompletions()`、`fang.WithoutManpage()` 两行即可恢复。

## 依赖 core 的接线

core 尚未发布 tag，本地开发用 `replace` 指向同级目录，两边的改动一起生效：

```
require github.com/tokenmp/core v0.0.0
replace github.com/tokenmp/core => ../core
```

core 发布 tag 后删掉 `replace`，把 `require` 改成具体版本即可。届时本仓库要按 tag
依赖，与 core 的公开面兼容承诺一致。core 是纯库，`dialect`、`relay`、`channel`、
`plugin`、`config`、`server` 六个包对外可见。

## 本机注意

这台机器连不上 `proxy.golang.org` 与 `sum.golang.google.cn`，取模块时会一直卡在
建立连接上。已在 `~/.config/go/env` 写入下面两项，构建与测试都能正常联网：

```
GOPROXY=https://goproxy.cn,direct
GOSUMDB=sum.golang.org
```

校验数据库的请求由 goproxy.cn 转发，因此 `GOSUMDB` 保持官方取值即可。

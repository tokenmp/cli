# tokenmp

TokenMP 的命令行工具。目前提供版本查询、启动服务与本地插件管理三组能力。

命令树、帮助页面、插件宿主与转发服务都来自开源项目，没有自己写参数解析、插件运行时与 HTTP 服务。

| 依赖 | 用途 |
| --- | --- |
| [spf13/cobra](https://github.com/spf13/cobra) | 命令树、子命令、`help` 子命令与 `-h` 标志 |
| [charmbracelet/fang](https://github.com/charmbracelet/fang) | 帮助与错误渲染，提供 `--version` 与 `-v` |
| [charmbracelet/lipgloss](https://charm.land/lipgloss) | 表格与字段的着色 |
| [charmbracelet/colorprofile](https://github.com/charmbracelet/colorprofile) | 按终端能力降级颜色 |
| [alecthomas/chroma](https://github.com/alecthomas/chroma) | 配置文本的语法高亮，bat 用的也是它 |
| [tokenmp/core](https://github.com/tokenmp/core) | 插件宿主、配置模型与插件接口定义 |

## 命令

| 命令 | 输出 | 示例 |
| --- | --- | --- |
| `tokenmp help` | 完整帮助。`tokenmp help plugin` 看命令组的说明 | `tokenmp help plugin check` |
| `tokenmp --help`、`tokenmp -h` | 完整帮助 | `tokenmp plugin --help` |
| `tokenmp --version`、`tokenmp -v` | 单行版本，例如 `tokenmp version dev (8c6f342)` | `tokenmp -v` |
| `tokenmp version` | 版本表，字段为 commit、built、go、platform | `tokenmp version` |
| `tokenmp run` | 启动转发服务，见下 | `tokenmp run --config /etc/tokenmp/config.yaml` |
| `tokenmp plugin <子命令>` | 本地插件管理，见下 | `tokenmp plugin list` |
| `tokenmp config <子命令>` | 查看与校验配置，见下 | `tokenmp config validate` |
| `tokenmp` | 不带参数时打印帮助 | `tokenmp` |

命令失败时以状态码 1 退出，错误信息由 fang 上色。中文错误文案不带句点，
也不把首词大写，这两处与 fang 的默认处理器不同。

## 运行服务

`tokenmp run` 按配置装配渠道与插件，然后开始监听，直到收到中断信号。

| 标志 | 作用 |
| --- | --- |
| `--config` | 配置文件路径，默认当前目录的 `config.yaml` |
| `--listen` | 覆盖配置里的 `listen`，例如 `--listen 127.0.0.1:9000` |
| `--dry-run` | 只做装配与预检，不监听 |

```
$ tokenmp run --config /etc/tokenmp/config.yaml
TokenMP 转发服务
配置:     /etc/tokenmp/config.yaml
监听:     127.0.0.1:8787
日志:     text / info
渠道:     1 个
模型规则: 1 条
插件:     0 个启用
插件目录: /etc/tokenmp/plugins（不存在）
端点:     /v1/chat/completions、/v1/responses、/v1/messages、/v1beta/models/{模型名}:{动作}、/healthz、/v1/models

按 Ctrl-C 停止：先停止接收新连接，再给在途请求最多 10 秒结束。
```

监听地址先建好监听器再打印，因此写 `--listen 127.0.0.1:0` 时报出的是真实端口。
服务日志按配置里的 `log` 段构造，写标准错误，与上面这份给人看的启动信息分开。

每个请求记三行，都写标准错误。默认级别是 `info`，因此服务在跑就能看见全部请求，
包括 `/healthz`、`/v1/models` 与未知路径：

```
level=INFO msg=请求进入 request_id=1 method=POST path=/v1/messages client=127.0.0.1 user_agent=curl/8.22.0
level=INFO msg=请求完成 request_id=1 method=POST path=/v1/messages status=200 duration=2.005676486s model=deepseek-chat channel=a attempts=1
level=INFO msg=用量统计 request_id=1 model=deepseek-chat channel=a prompt_tokens=500 completion_tokens=6 total_tokens=506 ttft=803.587741ms tps=5
```

前两行由 core 写：`请求进入` 在请求进来时写出，`请求完成` 在处理器返回后写出，状态码
小于 400 记 INFO，其余记 WARN。`attempts` 是这次请求试过的候选路由条数。一次请求产生的
失败行与完成行共用一个 `request_id`，报错时用它在日志里筛出整条链路。

第三行由本工具写：core 通过 `OnRequestDone` 交出原始量，口径定在
[internal/metrics](internal/metrics/metrics.go)。

| 字段 | 口径 |
| --- | --- |
| `prompt_tokens`、`completion_tokens`、`total_tokens` | 上游给出的用量 |
| `cached_tokens` | 命中提示缓存的那部分，有命中时才写 |
| `ttft` | 首字节延迟，只对流式请求写。非流式响应一次写完，这个数约等于总时长 |
| `tps` | 生成速率，分母是生成时长（总时长减 `ttft`），保留一位小数。预填充与排队不算进去，因此长提示词上不会偏低；非流式请求不写 |

`log.format: json` 时这些字段是结构化的，便于直接收集。用量与指标来自上游的响应，
同方言透传不解析响应体，因此这类请求只有前两行。

启动前会拦下三类问题：配置不合法，凭据环境变量没有设置的渠道，以及配置里启用
但插件目录里找不到的插件。三种都直接退出，不会带着问题开始监听。

服务提供的端点：

| 端点 | 方言 |
| --- | --- |
| `/v1/chat/completions` | OpenAI Chat |
| `/v1/responses` | OpenAI Responses |
| `/v1/messages` | Claude Messages |
| `/v1beta/models/{模型名}:{动作}` | Gemini，动作是 `generateContent` 或 `streamGenerateContent` |
| `/healthz`、`/v1/models` | 存活探测与模型列表 |

跨协议转换遇到 data URL 形式的图片时能自己解析，带 `;base64` 与百分号编码两种写法
都认。按 URL 取回图片内容的能力还没有实现，这类请求会明确报错，不会静默丢掉图片。

## 配置

配置是单个 YAML 文件，解码时开启 `KnownFields`，拼错的字段在加载时报错。相对路径
以配置文件所在目录为基准。

| 子命令 | 作用 | 示例 |
| --- | --- | --- |
| `config path` | 配置文件路径、状态与插件目录 | `tokenmp config path --json` |
| `config show` | 打印生效配置，默认是高亮文本 | `tokenmp config show --format yaml` |
| `config validate` | 校验并列出全部问题，不通过时退出码 1 | `tokenmp config validate` |
| `config get <路径>` | 按点分路径取一个字段 | `tokenmp config get channels.0.base_url` |
| `config set <路径> <值>` | 改一个标量字段 | `tokenmp config set log.level debug` |
| `config channels` | 列出渠道，凭据只显示环境变量名 | `tokenmp config channels` |
| `config models` | 列出路由规则，或按模型名试算选路 | `tokenmp config models --model gpt-4o` |
| `config init` | 生成配置骨架 | `tokenmp config init --config /etc/tokenmp/config.yaml` |

两个标志对整组生效：

| 标志 | 默认值 | 含义 |
| --- | --- | --- |
| `--config` | `config.yaml` | 配置文件路径 |
| `--json` | 关 | 机器可读输出，除 `config init` 外都支持 |
```
tokenmp config path
tokenmp config show
tokenmp config show --format yaml > /tmp/effective.yaml
tokenmp config validate --json
tokenmp config get listen
tokenmp config get plugins.list
tokenmp config set log.level debug
tokenmp config set plugins.dir /srv/tokenmp/plugins
tokenmp config channels
tokenmp config models --model claude-sonnet-4
tokenmp config init --config /etc/tokenmp/config.yaml
```

模型规则写在 `models` 段，一条规则把一类模型名绑到一个渠道：

| 字段 | 含义 |
| --- | --- |
| `match` | 模型名匹配式，支持 `*`（任意长度，含 `/` 与 `:`）与 `?`（一个字符） |
| `channel` | 命中的请求发往哪个渠道 |
| `upstream` | 发往上游的模型名。留空就用请求里的名字；写 `${model}` 会替换成请求里的名字 |
| `dialect` | 覆盖渠道方言，用于同一个基址上的多种协议 |
| `affinity` | 渠道亲和的组名。同组候选在运行时可互换，同一段会话粘在一条上；留空表示不参与 |

```
$ tokenmp config models --model deepseek-chat
路由规则
 匹配        渠道       上游模型           方言              亲和
 deepseek-*  account-a  deepseek/${model}  openai（同渠道）  accounts
 deepseek-*  account-b  同请求的模型名     openai（同渠道）  accounts

模型 deepseek-chat 的候选路由
 顺序  渠道       上游模型                方言    亲和
 1     account-a  deepseek/deepseek-chat  openai  accounts
 2     account-b  deepseek-chat           openai  accounts
```

试算里的上游模型名是替换之后的结果，规则表里给的是配置里的原样写法。

`config show` 支持三种格式，用 `--format` 选，默认是 `ui`：

| 格式 | 输出 |
| --- | --- |
| `ui` | 高亮的配置文本：YAML 按语法上色，左边一列行号，顶上一条表头 |
| `yaml` | 不带装饰的配置文本，可以重定向成一份新配置 |
| `json` | 机器可读的形状，`--json` 等同于此项 |

高亮用 chroma，bat 用的也是它，主题按终端明暗在 monokai 与 monokailight 之间选。
输出不是终端时 `ui` 与 `yaml` 完全一致，行号与表头不会混进重定向的文件。

```
$ tokenmp config show
─── /etc/tokenmp/config.yaml ─────────────────────────────────────────────
 1 │ listen: 127.0.0.1:8787
 2 │ log:
 3 │   format: text
 4 │   level: info
 5 │ channels:
 6 │   - name: openrouter
 7 │     dialect: openai
 8 │     base_url: https://openrouter.ai/api/v1
 9 │     auth:
10 │       style: bearer
11 │       key_env: OPENROUTER_API_KEY
12 │     timeout: 2m0s
13 │     max_retries: 0
14 │ models:
15 │   - match: gpt-*
16 │     channel: openrouter
17 │ plugins:
18 │   dir: /etc/tokenmp/plugins
19 │   list:
20 │     - key: echo
21 │       enabled: true
22 │       order: 10
23 │       config:
24 │         tag: example
```

`show` 与 `get` 看到的是填过默认值之后的生效配置，不是文件原文，因此没写在文件里的
`listen`、`timeout`、`max_retries` 也会出现。`yaml` 与 `json` 两种格式不打印空值字段：
空字符串、空段落与空列表只表示没写，没有信息量。

`get` 的路径与配置文件同形，数字段表示数组下标，可以取到 `channels.0.base_url` 这样的
嵌套取值。取到的段落或列表按 JSON 输出。

`set` 只改标量字段：路径必须已经存在，或者属于 `listen`、`log.format`、`log.level`、
`plugins.dir` 这四个顶层标量。指向段落或列表的路径会被拒绝。改写保留注释与缩进，
写回后重新加载校验，不合法就还原原文件，因此把 `log.level` 改成不认识的取值会失败，
而不是留下一份坏配置。

`channels` 只报告凭据所在的环境变量有没有设置，不读也不打印它的值。缺凭据的渠道会
在表下列出。

渠道与模型规则的增删改还没有对应命令，这几处结构复杂，直接在配置文件里写更直接。

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
| [cmd/config.go](cmd/config.go) | `config` 命令组与共用标志 |
| [cmd/config_path.go](cmd/config_path.go) | 配置文件路径与状态 |
| [cmd/config_show.go](cmd/config_show.go) | 生效配置，填过默认值并去掉空值 |
| [cmd/config_validate.go](cmd/config_validate.go) | 校验并列出全部问题 |
| [cmd/config_get.go](cmd/config_get.go) | 按路径取值 |
| [cmd/config_set.go](cmd/config_set.go) | 改写一个标量字段 |
| [cmd/config_channels.go](cmd/config_channels.go) | 渠道表与凭据状态 |
| [cmd/config_models.go](cmd/config_models.go) | 路由规则与选路试算 |
| [cmd/config_init.go](cmd/config_init.go) | 生成配置骨架 |
| [cmd/style.go](cmd/style.go) | 配色、表格与字段排版 |
| [cmd/errors.go](cmd/errors.go) | 错误渲染，去掉了英文句点与首词大写 |
| [internal/pluginmgr](internal/pluginmgr/env.go) | 配置、插件目录与宿主的装配 |
| [internal/configmgr](internal/configmgr/configmgr.go) | 配置的加载、路径取值与标量改写 |
| [internal/configedit](internal/configedit/configedit.go) | 保留注释的配置改写与校验回滚 |
| [internal/highlight](internal/highlight/highlight.go) | 配置文本的语法高亮，主题按终端明暗选 |
| [internal/logging](internal/logging/logging.go) | 按配置里的 log 段构造服务日志 |
| [internal/media](internal/media/media.go) | 媒体解析回调，目前只解析 data URL |
| [internal/metrics](internal/metrics/metrics.go) | 账目与指标行的口径：TTFT、TPS |
| [internal/version](internal/version/version.go) | 构建元数据与回退逻辑 |

帮助里只出现 `help`、`version`、`plugin` 与 `config`。cobra 自带的 `completion` 与 fang
自带的隐藏 `man` 命令在 [cmd/root.go](cmd/root.go) 里关掉了，去掉
`fang.WithoutCompletions()`、`fang.WithoutManpage()` 两行即可恢复。

## 依赖 core 的接线

`go.mod` 里写的是发布用的版本，开发时靠一个不提交的 `go.work` 指到同级的 core：

```
# go.mod，发布构建看到的就是这一行
require github.com/tokenmp/core v0.1.0
```

```
# go.work，只在本机存在，已在 .gitignore 里
use (
	.
	../core
)
```

同目录下同时检出两个仓库时，跑一次就有本地开发环境：

```
go work init . && go work use ../core
```

两种路径都实测过：带 `go.work` 时 `go list -m -f '{{.Dir}}' github.com/tokenmp/core`
指向本地目录；新克隆的仓库没有 `go.work`，依赖落到 v0.1.0，`go mod tidy && go build`
直接可用。发布构建因此不需要额外步骤，也不会因为本机存在同级目录而悄悄换掉依赖。

升级 core 时改 `require` 里的版本号，并重跑测试。core 是纯库，`dialect`、`relay`、
`channel`、`plugin`、`config`、`server` 六个包对外可见，公开面一旦发布就是兼容承诺。

## 本机注意

这台机器连不上 `proxy.golang.org` 与 `sum.golang.google.cn`，取模块时会一直卡在
建立连接上。已在 `~/.config/go/env` 写入下面两项，构建与测试都能正常联网：

```
GOPROXY=https://goproxy.cn,direct
GOSUMDB=sum.golang.org
```

校验数据库的请求由 goproxy.cn 转发，因此 `GOSUMDB` 保持官方取值即可。

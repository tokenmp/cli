# tokenmp

TokenMP 的命令行工具。目前提供版本查询、启动与测试转发服务、配置查看、渠道与模型规则管理，以及本地插件管理几组能力。

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
| `tokenmp test` | 进程内起服务发一条测试请求，见下 | `tokenmp test --model deepseek-chat --dialect claude` |
| `tokenmp channels <子命令>` | 渠道的增删查，见下 | `tokenmp channels list` |
| `tokenmp models <子命令>` | 模型路由规则的增删查，见下 | `tokenmp models list --model gpt-4o` |
| `tokenmp import` | 从 new-api 导出的渠道列表追加渠道与规则，见下 | `tokenmp import --from new-api --file channels.json` |
| `tokenmp plugin <子命令>` | 本地插件管理，见下 | `tokenmp plugin list` |
| `tokenmp config <子命令>` | 查看与校验配置，见下 | `tokenmp config validate` |
| `tokenmp completion <子命令>` | 生成并安装 shell 补全，见下 | `tokenmp completion install zsh` |
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
| `prompt_tokens`、`completion_tokens`、`total_tokens` | 上游给出的用量。提示词总数含缓存读与缓存写 |
| `cache_read_tokens` | 命中提示缓存的提示词数 |
| `cache_write_tokens` | 写进提示缓存的提示词数，Claude 与 OpenAI 两种上报写法已归一 |
| `cache_hit_rate` | 缓存读除以提示词总数，保留三位小数，取值在 0 到 1 之间 |
| `ttft` | 首字节延迟，只对流式请求写。非流式响应一次写完，这个数约等于总时长 |
| `tps` | 生成速率，分母是生成时长（总时长减 `ttft`），保留一位小数。预填充与排队不算进去，因此长提示词上不会偏低；非流式请求不写 |

分子只算缓存读：缓存写是新写入的部分，不算命中，但它计入分母（提示词总数含两者）。
上游不报缓存用量时这两个数也是零，与「报了但没命中」分不开，因此三个字段宁可不写，
也不写一个可能读错意思的 0。

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

## 测试服务

`tokenmp test` 回答「这条渠道现在能不能用」。`config validate` 只查语法，
`channels list` 只报凭据有没有设，都不发请求；`test` 在进程内按配置装配同一套服务，
监听 `127.0.0.1` 的随机端口，按选定的客户端方言发一条最小请求，拿到结果后关掉。
选路、跨方言转换、重试与插件都会走一遍，比直接打渠道更接近真实使用。

| 标志 | 默认值 | 含义 |
| --- | --- | --- |
| `--config` | `config.yaml` | 配置文件路径 |
| `--model` | 第一条路由规则的 `match` | 要测试的模型名 |
| `--prompt` | `你好` | 发给上游的提示词 |
| `--dialect` | `openai` | 客户端方言：`openai`、`responses`、`claude`、`gemini` |
| `--stream` | 关 | 按流式发送请求 |
| `--timeout` | `30s` | 整条测试请求的超时时间 |
| `--raw` | 关 | 打印完整的响应体 |
| `--json` | 关 | 以 JSON 输出结构化结果 |
| `--verbose` | 关 | 按配置里的级别打印 core 的服务日志 |

先打印配置与候选路由，再打印请求结果。候选路由取自 `config.Resolve`，与
`models list --model` 试算的是同一份结果，因此列表里的上游模型是 `${model}` 替换
之后的。结果里的渠道、尝试次数与上游模型取自 core 的 `OnRequestDone`，TTFT 与 TPS 的
口径复用 [internal/metrics](internal/metrics/metrics.go)。

```
$ tokenmp test --model deepseek-chat --prompt 你好 --dialect claude
配置:   /tmp/tokenmp/config.yaml
客户端: claude（POST /v1/messages）
模型:   deepseek-chat

候选路由
 顺序  渠道         上游模型                方言    亲和
 1     commandcode  deepseek/deepseek-chat  openai  accounts
 2     backup       deepseek-chat           openai  -

请求结果
渠道:     commandcode（第 1 条候选，尝试 1 次）
上游模型: deepseek/deepseek-chat
状态:     200
耗时:     1ms
用量:     prompt=12 completion=34 total=46
回答:     你好！有什么可以帮你的？
```

`--raw` 在结果下面多打印一段完整的响应体。`--json` 给结构化结果，形状里有 `routes` 与
`result` 两段，`result.channel` 与 `result.upstream` 是实际命中的渠道与上游模型，
便于脚本断言。

默认把 core 的服务日志压到 `warn`，只显示结果；`--verbose` 时按配置里的级别放开。
上游返回非 2xx、超时、装配失败或模型没有匹配规则时以状态码 1 退出，并打印上游错误体。

`test` 只发一条最小请求，不做并发压测，也不能用 `--channel` 直接打某条渠道。

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
tokenmp config init --config /etc/tokenmp/config.yaml
```

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

渠道与模型规则的增删查分别在下面两节。

## 渠道

`tokenmp channels` 管理配置 `channels` 段里的上游渠道。

| 子命令 | 作用 | 示例 |
| --- | --- | --- |
| `channels list` | 列出渠道与凭据状态 | `tokenmp channels list --json` |
| `channels create` | 追加一个渠道 | `tokenmp channels create --name openrouter --dialect openai --base-url https://openrouter.ai/api/v1 --path /chat/completions --key-env OPENROUTER_API_KEY` |
| `channels delete`、`channels remove` | 删除一个渠道 | `tokenmp channels delete openrouter` |

`create` 追加前按渠道规则校验，例如 `base_url` 带了路径却没给 `path`、鉴权方式与参数对不上，都在写文件之前拦下。`--auth-style` 不写时按 `--key-env` 推断：给了就是 `bearer`，没给就是 `none`。渠道名重复时报错，不覆盖。删除仍被 `models` 段引用的渠道会被拒绝，并列出引用它的规则。

`list` 只报告凭据所在的环境变量有没有设置，不读也不打印它的值。缺凭据的渠道会在表下列出。

`tokenmp channels` 不带子命令时进入交互界面。↑/↓ 或 j/k 移动，PgUp/PgDn 翻页，
Home/End 跳到首尾，选中行下方显示九个字段的详情，按 d 删除并确认，按 q 或 Ctrl-C 退出。
删除仍被 `models` 段引用的渠道会被拒绝，并在状态栏里指出是哪几条规则。
界面上只改内存里的列表，删除成功才写回配置，写回保留注释与缩进，取消或直接退出都不会动文件。

输出不是终端时，`tokenmp channels` 打印与 `channels list` 相同的表格，不做交互。
`--json` 与交互互斥：管道里给 `--json` 输出 JSON，与 `channels list --json` 相同；
进交互时给 `--json` 会报错。

```
$ tokenmp channels list
 渠道        方言    基址                          路径               鉴权                         凭据    超时  重试
 example     openai  https://api.example.com/v1    /chat/completions  bearer ← EXAMPLE_API_KEY     未设置  2m0s  0
 openrouter  openai  https://openrouter.ai/api/v1  /chat/completions  bearer ← OPENROUTER_API_KEY  未设置  2m0s  0

凭据环境变量没有设置：example、openrouter
```

## 模型规则

`tokenmp models` 管理配置 `models` 段里的路由规则。一条规则把一类模型名绑到一个渠道：

| 字段 | 含义 |
| --- | --- |
| `match` | 模型名匹配式，支持 `*`（任意长度，含 `/` 与 `:`）与 `?`（一个字符） |
| `channel` | 命中的请求发往哪个渠道 |
| `upstream` | 发往上游的模型名。留空就用请求里的名字；写 `${model}` 会替换成请求里的名字 |
| `dialect` | 覆盖渠道方言，用于同一个基址上的多种协议 |
| `affinity` | 渠道亲和的组名。同组候选在运行时可互换，同一段会话粘在一条上；留空表示不参与 |

| 子命令 | 作用 | 示例 |
| --- | --- | --- |
| `models list` | 列出规则，或按模型名试算选路 | `tokenmp models list --model gpt-4o` |
| `models create` | 追加一条规则 | `tokenmp models create --match "deepseek-*" --channel commandcode --upstream "deepseek/${model}"` |
| `models delete`、`models remove` | 按序号或匹配式删除一条规则 | `tokenmp models delete 0` |

规则没有名字，`list` 表里的序号是规则在 `models` 段里的下标，与 `config get models.0` 的口径一致，`delete` 收这个序号。按内容删时给 `match` 的原样写法，规则有多条时用 `--channel` 指定其中一条；删除后打印删掉的那条的匹配式与渠道。`create` 引用了不存在的渠道时报错，同一条 `match` 与 `channel` 组合重复时报错，不覆盖。

`tokenmp models` 不带子命令时同样进入交互界面。列表列与 `models list` 一致，
选中行下方显示规则的五个字段，按 d 删除并确认，按 q 或 Ctrl-C 退出。
界面上只改内存里的列表，删除成功才写回配置，取消或直接退出都不会动文件。

输出不是终端时，`tokenmp models` 打印与 `models list` 相同的规则表。
`--json` 与交互互斥：管道里给 `--json` 输出 JSON，与 `models list --json` 相同；
进交互时给 `--json` 会报错。

```
$ tokenmp models list --model deepseek-chat
路由规则
 序号  匹配        渠道        上游模型           方言              亲和
 0     deepseek-*  account-a   deepseek/${model}  openai（同渠道）  accounts
 1     deepseek-*  account-b   同请求的模型名     openai（同渠道）  accounts

模型 deepseek-chat 的候选路由
 顺序  渠道        上游模型                方言    亲和
 1     account-a   deepseek/deepseek-chat  openai  accounts
 2     account-b   deepseek-chat           openai  accounts
```

试算里的上游模型名是替换之后的结果，规则表里给的是配置里的原样写法。

## 从 new-api 导入

`tokenmp import` 读一份 new-api 渠道列表接口的响应 JSON，往配置里追加对应的渠道与模型规则。它只吃文件，不连 new-api 的 API、也不读它的数据库。导入改的是配置文件，下一次 `run` 才生效。

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `--from` | 无 | 数据源，目前只支持 `new-api` |
| `--file` | 无 | 导出的 JSON 路径，渠道在响应信封的 `data.items` 里 |
| `--config` | `config.yaml` | 配置文件路径 |
| `--dry-run` | 关 | 只打印将要写入的渠道数、规则数与需要的环境变量名，不落盘 |

new-api 后台没有导出渠道的入口，列表 JSON 要自己取：

```
curl -H "Authorization: Bearer <root token>" https://new-api.example.com/api/channel/ > channels.json
```

最小的一份样例：

```json
{
  "success": true,
  "data": {
    "items": [
      {
        "id": 1,
        "name": "openai",
        "type": 1,
        "key": "",
        "base_url": "https://api.openai.com",
        "models": "gpt-4o,gpt-4o-mini"
      },
      {
        "id": 2,
        "name": "openrouter",
        "type": 20,
        "key": "",
        "base_url": "https://openrouter.ai/api",
        "models": "anthropic/claude-sonnet-4"
      }
    ],
    "total": 2,
    "page": 1,
    "page_size": 100
  }
}
```

导入并验证：

```
# 先看将要写什么
$ tokenmp import --from new-api --file channels.json --dry-run

# 写进配置
$ tokenmp import --from new-api --file channels.json --config config.yaml

# 依次验证：配置合法、渠道列出、发一条测试请求
$ tokenmp config validate --config config.yaml
$ tokenmp channels list --config config.yaml
export OPENAI_API_KEY=...
$ tokenmp test --config config.yaml --model gpt-4o
```

类型到方言的映射：

- OpenAI 兼容的渠道归 `openai`，包含 `1`、`6`、`7`、`9`、`10`、`12`、`13`、`19`、`20`、`22`、`25`、`31`、`40`、`42`、`43`、`47`、`48`、`53`
- `14` Anthropic 归 `claude`
- `24` Gemini 与 `41` Vertex AI 归 `gemini`
- 其余类型（Midjourney、Azure、Perplexity、Cohere、Bedrock 等）不在表里，导入会报错并指出是哪一条渠道，不会静默归到 `openai`

`base_url` 带了路径时会按它算出要写进配置的 `path`：`https://openrouter.ai/api` 配 `/v1/chat/completions`，`https://api.openai.com/v1` 配 `/chat/completions`，两种写法指向同一个上游端点。算不出来（例如 Gemini 的路径含模型名）就跳过并说明。`models` 字段按逗号切分，每个模型写一条规则，`channel` 指向该渠道；渠道名重复时跳过并说明，不写重复条目。

密钥要单独说一句。列表接口查询时 `Omit("key")`，JSON 里 `key` 恒为空串，拿不到明文密钥，因此导入只写 `key_env`，变量值由你自己设，输出里会列出需要设置哪些变量。变量名由渠道名推导，例如 `commandcode` 推出 `COMMANDCODE_API_KEY`；与已用的名字冲突时接 `_2`、`_3`，并在输出里说明。即使 JSON 里手工填了 `key`，也不会写进配置。

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

## Shell 补全

`tokenmp completion <shell>` 把补全脚本写到标准输出，`tokenmp completion install` 直接
装进 shell 的查找目录。支持 bash、zsh、fish 与 powershell 四种 shell。

```
tokenmp completion install            # 不指定则按 $SHELL 猜
tokenmp completion install zsh
tokenmp completion install bash --dir ~/.config/bash-completion/completions
tokenmp completion install bash --dry-run
```

不指定 shell 时按 `$SHELL` 的基名猜，猜不出会报错并列出可选值。落点按 XDG 约定：

| shell | 落点 |
| --- | --- |
| bash | `$XDG_DATA_HOME/bash-completion/completions/tokenmp` |
| zsh | `$XDG_DATA_HOME/zsh/site-functions/_tokenmp` |
| fish | `$XDG_CONFIG_HOME/fish/completions/tokenmp.fish` |
| powershell | 没有补全查找目录，只在 `$PROFILE` 里生效，命令只打印提示不写文件 |

`$XDG_DATA_HOME` 默认 `~/.local/share`，`$XDG_CONFIG_HOME` 默认 `~/.config`，
前三条与 `mise completion --install` 的落点一致。目录不存在时创建，文件已存在时覆盖，
重复执行结果相同；`--dry-run` 只看将要写入的路径，不落盘。

zsh 的落点不在默认 `fpath` 里，装完要把下面一行加进 `~/.zshrc`：

```
fpath+=${XDG_DATA_HOME:-$HOME/.local/share}/zsh/site-functions
```

PowerShell 没有补全查找目录，只能把下面一行加进 `$PROFILE`：

```
tokenmp completion powershell | Out-String | Invoke-Expression
```

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
| [cmd/run.go](cmd/run.go) | `run` 子命令与服务装配 |
| [cmd/test.go](cmd/test.go) | `test` 子命令：进程内起服务发一条测试请求 |
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
| [cmd/channels.go](cmd/channels.go) | `channels` 命令组与渠道整理的共用逻辑 |
| [cmd/channels_list.go](cmd/channels_list.go) | 渠道表与凭据状态 |
| [cmd/channels_create.go](cmd/channels_create.go) | 追加渠道并校验 |
| [cmd/channels_delete.go](cmd/channels_delete.go) | 删除渠道，拒绝删除被引用的渠道 |
| [cmd/browse.go](cmd/browse.go) | 交互式浏览与删除的界面模型，channels 与 models 共用 |
| [cmd/models.go](cmd/models.go) | `models` 命令组与规则整理的共用逻辑 |
| [cmd/models_list.go](cmd/models_list.go) | 路由规则表与选路试算 |
| [cmd/models_create.go](cmd/models_create.go) | 追加路由规则 |
| [cmd/models_delete.go](cmd/models_delete.go) | 按序号或匹配式删除规则 |
| [cmd/import.go](cmd/import.go) | 从 new-api 渠道列表 JSON 导入渠道与规则 |
| [cmd/config_init.go](cmd/config_init.go) | 生成配置骨架 |
| [cmd/completion.go](cmd/completion.go) | shell 补全：输出脚本与 install |
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

帮助里出现 `help`、`version`、`run`、`test`、`channels`、`models`、`import`、`plugin`、
`config` 与 `completion`。fang
自带的隐藏 `man` 命令在 [cmd/root.go](cmd/root.go) 里关掉了，去掉
`fang.WithoutManpage()` 一行即可恢复。cobra 的 `completion` 在
[cmd/completion.go](cmd/completion.go) 里补了中文说明，并挂上 `install` 子命令。

## 依赖 core 的接线

`go.mod` 里写的是发布用的版本，开发时靠一个不提交的 `go.work` 指到同级的 core：

```
# go.mod，发布构建看到的就是这一行
require github.com/tokenmp/core v0.5.0
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
指向本地目录；新克隆的仓库没有 `go.work`，依赖落到 v0.5.0，`go mod tidy && go build`
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

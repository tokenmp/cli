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
| `tokenmp accounts <子命令>` | 顶层账号的增删查，见下 | `tokenmp accounts list` |
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

### 调用记录推送与客户端密钥校验

配置里写了 `records` 段时，`tokenmp run` 会做三件事：把每次调用推给 web 的摄入端点；在转发之前拿客户端密钥调 web 的校验端点，把身份填进记录；配了 `cost` 段时再按用量与两侧计价口径算金额，成功且收入大于 0 的调用多推一行 `billing`。不写 `records` 段就都不做，`run` 的行为与没有它时一样。

```yaml
records:
  endpoint: "http://127.0.0.1:6789/api/internal/records"
  secret_env: "RECORDS_CALLBACK_SECRET"
  batch_size: 200
  flush_interval: 5s
  cache_ttl: 1m
  fail_closed: true
```

| 字段 | 默认值 | 含义 |
| --- | --- | --- |
| `endpoint` | 必填 | 摄入端点地址 |
| `auth_endpoint` | 从 `endpoint` 推出 | 客户端密钥校验端点地址。不写时把 `endpoint` 结尾的 `/records` 换成 `/api-key` |
| `secret_env` | 必填 | 共享密钥所在的环境变量名，两个端点共用 |
| `batch_size` | `200` | 一批最多的行数 |
| `flush_interval` | `5s` | 攒不满一批时的兜底发送间隔 |
| `cache_ttl` | `1m` | 一把密钥的校验结果缓存多久 |
| `fail_closed` | `true` | 校验端点不通时拒绝还是放行 |

密钥值只从 `secret_env` 指的环境变量读，变量没设置时启动就报错，与渠道凭据同一规矩。两个端点的请求都带 `x-callback-secret` 请求头，客户端密钥明文只放校验请求的请求体，不放 URL。

推送接在写账目行的同一处 `OnRequestDone` 上，两边共用 core 交出的同一份原始量。只推四种方言入口的模型调用，`/healthz` 与 `/v1/models` 这类探活不会被推送。攒批在内存里做，发送失败按退避重试（1 秒起步，每次翻倍，最多 30 秒），不挡转发。内存队列上限 10000 行，满了丢最旧的一条并记 WARN。Ctrl-C 之后先停止转发，再用最多 10 秒把队列里剩下的发完，超时记 WARN 放弃。对端按 `request_id` 幂等，重试时整批重发，cli 不记哪些发过。

字段口径见 web 仓库的 `docs/forwarding-ingest.md`。推送一次带两个数组：`records` 每次都发，`billing` 只在调用成功且收入大于 0 时才有。两者同一批 POST，web 独立插入，靠 `request_id` 关联，不要求有序。

#### 金额与 billing

`billing` 的金额由本机算，算完只交用量与金额，`ledger` 由 web 在摄入时按用户当期的订阅余量判定。收入侧的口径来自密钥校验端点：套餐是 `markup` 模式时收入是成本乘 `markup_multiplier`，是 `table` 模式时按该套餐的模型价目算。成本单价是 cli 自己的配置，落在 `cost` 段，每条渠道一组，`cost_models` 按模型覆盖：

```yaml
cost:
  currency: CNY
  channels:
    openrouter:
      cost_input_per_million: 2.5
      cost_output_per_million: 10
      cost_cache_read_per_million: 0.25
      cost_cache_write_per_million: 1.25
      cost_models:
        gpt-5:
          input_per_million: 5
          output_per_million: 15
    pool/team-a:
      cost_mode: request
      cost_per_request: 0.02
```

| 字段 | 默认值 | 含义 |
| --- | --- | --- |
| `currency` | `CNY` | 金额币种，写进 billing 行 |
| `channels` | 必填 | 按展开后的渠道名索引的上游成本单价 |
| `channels.<渠道>.cost_mode` | `token` | `token` 按 token 单价算，`request` 只看 `cost_per_request` |
| `cost_input_per_million` 等四个 | `0` | 每百万 token 的单价 |
| `cost_per_request` | `0` | 按次计费的单价 |
| `cost_models` | 无 | 按模型的覆盖，写了就整组替换渠道级单价，没写 `cost_mode` 时继承 |

计价一律先把缓存读与缓存写从提示词里扣掉：core 归一之后 `prompt_tokens` 包含这两部分，不扣的话命中的 token 会先按输入价收一遍、再按缓存价收一遍。成本与收入各记一次，两列倍率规则记下命中的渠道与套餐，以后对账能回答「这笔为什么是这个倍率」。

`table` 模式的价目表按用户看到的模型名建，而 core 交出的 `Outcome.Model` 是插件改写之后参与匹配的名字，直接查不到时按 cli 的模型规则反查：找 `upstream` 等于它的那条，用它的 `match` 再查一次。没套餐、价目表缺这个模型、或 `cost` 段没配这条渠道时算不出来，只发 `records`，并记一条 WARN。没配 `cost` 段就只发 `records`，不发 `billing`。

### 客户端密钥校验

配了 `records` 段的 `tokenmp run` 在转发之前先认调用方：按方言从请求里取客户端密钥，调 web 的 `POST /api/internal/api-key` 换成身份，把 `user_id` 与 `api_key_id` 填进这次的记录。`tokenmp test` 不走这条校验：它在本机发测试请求，没有客户端密钥，考的是上游转发链路。

密钥按方言语境取：OpenAI 与 Claude 认 `Authorization: Bearer` 与 `x-api-key`，Gemini 认 `x-goog-api-key` 与查询串里的 `key`。校验结果按密钥明文的 sha256 缓存，不拿明文当键；同一把密钥在 `cache_ttl` 内只打一次校验端点，被拒的结果同样缓存。缓存有 4096 条上限，到顶先清过期的，还满就丢一条。

校验端点按原因回：未知密钥 `404`、密钥或账号停用 `403`、密钥过期 `410`。这三个码只进内核自己的 WARN 日志（带 `reason` 文字），**对外一律回 `401`**：404 看起来像路径不存在，各家 SDK 不会按认证失败处理，不会提示重新填密钥。给客户端的错误体里 `code` 统一是 `invalid_api_key`，`message` 保留具体原因（「密钥不存在」「密钥或账号已停用」「密钥已过期」），客户端据此自己看得出是不是过期了。错误体与 core 一样是 `{"error":{"message","type","code"}}`。`model_whitelist` 为空表示不限制模型，写了名单而请求的模型不在里面时回 `403`（密钥有效，是没有权限）。被拦下的请求不写记录：认不出来是谁，也没有上游渠道，写进去只会污染全站视图的统计。拦下的原因（不是密钥明文，是它的短哈希）与累计条数记 WARN。

校验端点不通（连接失败、超时、5xx、429）时按 `fail_closed` 走：默认拒绝，回 `503` 与 `identity_unavailable`，并记一条带 `unreachable_total` 计数的 WARN，不静默。调试时把 `fail_closed` 设成 `false`，端点不通就放行但不带身份，记录里两列为空。单次校验请求超时 2 秒，失败后隔 200 毫秒再试一次。

端点返回的 `rate_limit_per_min` 收进了缓存结构，但本 issue 不启用：内核现在没有客户端限速，那是另一件事。

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
| `config path` | 配置文件路径、状态与顶层标量 | `tokenmp config path --json` |
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

`path` 给出配置文件的绝对路径、是否已存在，以及 `listen`、`log.format`、
`defaults.max_body_bytes` 这些顶层标量的生效取值 —— 整份配置只取一个值的键。标量一栏由
core 的键表现算，标了 `（默认值）` 的是文件里没写、由 core 兜底的项；文件不存在时也照常
输出，因此脚本可以直接从这里取路径。

`set` 只改标量字段：路径必须已经存在，或者属于 `listen`、`log.format`、`log.level`、
`plugins.dir` 这四个顶层标量。指向段落或列表的路径会被拒绝。取值按键表里的类型编码，
整数字段给 `1048576` 会写成数字而不是字符串。改写保留注释与缩进，写回后重新加载校验，
不合法就还原原文件，因此把 `log.level` 改成不认识的取值会失败，而不是留下一份坏配置。

`init` 写出的骨架由同一份键表现算：`listen`、`log`、`plugins.dir` 这些取 core 的默认值
（`log.format` 例外，骨架给 `text`，方便刚上手时在终端里看），`channels`、`models` 与
`plugins.list` 是示例段，要自己改。凭据只写环境变量名，不写密钥。

渠道与模型规则的增删查分别在下面两节。

## 渠道

`tokenmp channels` 管理配置 `channels` 段里的上游渠道。

| 子命令 | 作用 | 示例 |
| --- | --- | --- |
| `channels list` | 列出渠道与凭据状态 | `tokenmp channels list --json` |
| `channels create` | 追加一个渠道 | `tokenmp channels create --name openrouter --dialect openai --base-url https://openrouter.ai/api/v1 --path /chat/completions --key-env OPENROUTER_API_KEY` |
| `channels delete`、`channels remove` | 删除一个渠道 | `tokenmp channels delete openrouter` |

`create` 追加前按渠道规则校验，例如 `base_url` 带了路径却没给 `path`、鉴权方式与参数对不上，都在写文件之前拦下。`--auth-style` 不写时按凭据来源推断：给了 `--key-env` 或 `--accounts` 就是 `bearer`，都没给就是 `none`。`--accounts` 给出顶层账号名（逗号分隔），与 `--key-env` 互斥，引用了不存在的账号会报错。渠道名重复时报错，不覆盖。删除仍被 `models` 段引用的渠道会被拒绝，并列出引用它的规则。

`list` 只报告凭据所在的环境变量有没有设置，不读也不打印它的值。缺凭据的渠道会在表下列出。

`tokenmp channels` 不带子命令时进入分步管理。它先打印与 `channels list` 相同的表格，
再问要做什么：新建、编辑、删除或退出。选编辑或删除时，还要从列表里挑一条。

选好动作后，一个字段一个问题地问下去。顺序是渠道名、方言、基址、路径、鉴权方式、
凭据环境变量名、自定义请求头或查询参数名、超时、最大重试次数。
编辑时每个字段的默认值是当前值，回车即保留。
鉴权方式为 `none` 时跳过凭据，`bearer` 与 `x-api-key` 只问环境变量名，
`custom` 问请求头名，`query` 问查询参数名。

每个字段当场校验：渠道名不能为空，基址要以 `http://` 或 `https://` 开头，
超时按 `time.ParseDuration` 的写法解析，最大重试次数在 0 到 10 之间，
鉴权方式与参数要对得上。全部问完后打印将要写入的条目，再确认一次才落盘。
写回只改变化过的字段，已有的注释与缩进保留，写回后配置不合法会自动还原。

删除仍被 `models` 段引用的渠道会被拒绝，并列出引用它的规则；被引用的渠道也不能改名。
做完一件回到菜单，可以接着做下一件，选退出才结束。
在字段表单里按 Esc 或 Ctrl-C 取消当前这件并回到菜单，在动作菜单里按则直接退出，
两种情况都不改文件。

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

## 顶层账号

`tokenmp accounts` 管理配置顶层 `accounts` 段里的账号。账号是顶层的凭据清单，渠道在
`auth.accounts` 里按名字引用它，加载时展开成多条渠道，展开名是「渠道名/账号名」。

| 子命令 | 作用 | 示例 |
| --- | --- | --- |
| `accounts list` | 列出账号、凭据状态、引用它的渠道与请求头数量 | `tokenmp accounts list --json` |
| `accounts create` | 追加一个账号，`--header` 可重复 | `tokenmp accounts create --name team-c --key-env TEAM_C_API_KEY --header x-workspace=team-c` |
| `accounts delete`、`accounts remove` | 删除一个账号 | `tokenmp accounts delete team-c` |

账号级请求头用于上游按 workspace 或 organization 隔离，是这一层存在的理由；
`--header k=v` 可重复，同名时账号的头覆盖渠道的头。删除仍被渠道在 `auth.accounts` 里
引用的账号会被拒绝，并列出引用它的渠道与它们在 `channels` 段里的下标。

`tokenmp accounts` 不带子命令时和 `channels` 一样进入分步管理：先打印与 `accounts list`
相同的表格，再问新建、编辑、删除或退出；每次只写变化过的字段，写回前再确认一次，
Esc 或 Ctrl-C 不落盘。输出不是终端时打印与 `accounts list` 相同的表格；`--json` 只在
非终端时生效，进交互时报错。

core 要求每个顶层账号都被至少一条渠道引用。新建账号到它被渠道引用之间是一段中间
状态，此时 `config validate` 与 `run` 会拒绝这份配置，但 `accounts` 命令与
`channels create` 仍能读写它，好让 `tokenmp accounts create` 之后直接用
`tokenmp channels create --accounts` 接上引用。

```
$ tokenmp accounts list
 账号    key_env        凭据    引用渠道  请求头
 team-a  TEAM_A_API_KEY  未设置  pool      1
 team-b  TEAM_B_API_KEY  未设置  pool      0

凭据环境变量没有设置：team-a、team-b
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

`tokenmp models` 不带子命令时同样进入分步管理。它先打印与 `models list` 相同的规则表，
再问要做什么：新建、编辑、删除或退出。

规则按 match、channel、upstream、dialect、affinity 的顺序一个字段一个问题地问。
channel 只能从配置里已有的渠道里选；match 用 core 的匹配式校验，必须非空；
upstream 只认 `${model}` 占位符；dialect 的第一项是留空，表示用渠道方言。
编辑时默认值是当前值，回车即保留。全部问完后打印将要写入的条目，再确认一次才落盘。
写回只改变化过的字段，注释与缩进保留。

没有渠道时命令会提示先建渠道；同一条 match 与 channel 组合重复时会拒绝。
做完一件回到菜单，可以接着做下一件，选退出才结束。
在字段表单里按 Esc 或 Ctrl-C 取消当前这件并回到菜单，在动作菜单里按则直接退出，
两种情况都不改文件。

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
| [cmd/accounts.go](cmd/accounts.go) | `accounts` 命令组与账号整理的共用逻辑 |
| [cmd/accounts_list.go](cmd/accounts_list.go) | 账号表、凭据状态与引用渠道 |
| [cmd/accounts_create.go](cmd/accounts_create.go) | 追加账号并校验 |
| [cmd/accounts_delete.go](cmd/accounts_delete.go) | 删除账号，拒绝删除被引用的账号 |
| [cmd/accounts_manage.go](cmd/accounts_manage.go) | 账号的分步新建、编辑与删除 |
| [cmd/channels.go](cmd/channels.go) | `channels` 命令组与渠道整理的共用逻辑 |
| [cmd/channels_list.go](cmd/channels_list.go) | 渠道表与凭据状态 |
| [cmd/channels_create.go](cmd/channels_create.go) | 追加渠道并校验 |
| [cmd/channels_delete.go](cmd/channels_delete.go) | 删除渠道，拒绝删除被引用的渠道 |
| [cmd/manage.go](cmd/manage.go) | 分步管理的共用流程：动作菜单、选条目与确认 |
| [cmd/channels_manage.go](cmd/channels_manage.go) | 渠道的分步新建、编辑与删除 |
| [cmd/models_manage.go](cmd/models_manage.go) | 模型规则的分步新建、编辑与删除 |
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
| [internal/configedit](internal/configedit/configedit.go) | 保留注释的配置改写与校验回滚，含序列条目字段改写 |
| [internal/highlight](internal/highlight/highlight.go) | 配置文本的语法高亮，主题按终端明暗选 |
| [internal/logging](internal/logging/logging.go) | 按配置里的 log 段构造服务日志 |
| [internal/media](internal/media/media.go) | 媒体解析回调，目前只解析 data URL |
| [internal/records](internal/records/sender.go) | 调用记录与计费行的组装、攒批与推送 |
| [internal/clientauth](internal/clientauth/identity.go) | 转发路径上认客户端密钥，带回身份与计价口径 |
| [internal/billing](internal/billing/calc.go) | 按用量与两侧计价口径算成本与收入，组装 billing 行 |
| [internal/metrics](internal/metrics/metrics.go) | 账目与指标行的口径：TTFT、TPS |
| [internal/version](internal/version/version.go) | 构建元数据与回退逻辑 |

帮助里出现 `help`、`version`、`run`、`test`、`accounts`、`channels`、`models`、`import`、`plugin`、
`config` 与 `completion`。fang
自带的隐藏 `man` 命令在 [cmd/root.go](cmd/root.go) 里关掉了，去掉
`fang.WithoutManpage()` 一行即可恢复。cobra 的 `completion` 在
[cmd/completion.go](cmd/completion.go) 里补了中文说明，并挂上 `install` 子命令。

## 依赖 core 的接线

`go.mod` 里写的是发布用的版本，开发时靠一个不提交的 `go.work` 指到同级的 core：

```
# go.mod，发布构建看到的就是这一行
require github.com/tokenmp/core v0.7.1
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
指向本地目录；新克隆的仓库没有 `go.work`，依赖落到 v0.7.1，`go mod tidy && go build`
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

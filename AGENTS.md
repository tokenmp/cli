# AGENTS.md

在本仓库工作的规则。

## 项目

TokenMP 的命令行工具，Go 可执行文件 `tokenmp`，模块 `github.com/tokenmp/cli`。
它装配 [core](https://github.com/tokenmp/core) 起转发服务，并负责客户端密钥鉴权、
算金额、把调用记录与计费流水推给 web。

```
cmd/                 一个文件一个命令组
internal/billing/    金额计算与 cost 段配置
internal/clientauth/ 客户端密钥校验与身份缓存
internal/configmgr/  配置的读写与键表
internal/configedit/ 交互式编辑的字段模型
internal/records/    推给 web 的发送端，攒批与重试
internal/media/      媒体内容的取回与体积限制
internal/metrics/    ttft、tps 的口径
```

## 命令

```
make check      # 门禁：编译、单测、静态检查、格式检查
make build      # 只做编译检查
make test       # 单测
make lint       # 静态检查
make fmt        # 格式化
make tools      # 按 .mise.toml 装齐工具链
```

改动后必须跑 `make check` 并保持全绿。该目标只依赖 Go 与 golangci-lint。

## 与 core 的边界

core 是库，cli 是宿主。两者职责不能互串：

- 协议转换、方言、渠道选路、重试、亲和 —— 全在 core，cli 不重新实现。
- config 的键表由 core 的 `config.Keys()` 现算（`config path`、`config set` 的帮助、
  `config init` 的骨架都从它生成）；哪条路径可 `set` 是**命令行策略**，由 cli 维护。
- 配置只描述事实，装配在 cli。

改 core 的公开面属于破坏性变更，要等 core 发新 tag 再升 `go.mod`。

## 依赖布局

发布用的版本写在 `go.mod` 的 `require` 里。开发时 `go.work` 指向同级的 core 目录，
该文件已在 `.gitignore` 中，不进仓库。新克隆的仓库没有 `go.work`，依赖落到 `require`
声明的版本，因此发布构建不会因为本机存在同级目录而换掉依赖。

## 约定

- 注释与用户可见文案使用中文。
- 注释写取舍与原因，不复述代码在做什么。
- 错误必须处理，响应体必须关闭。
- 工具链版本只在 `.mise.toml` 中声明，不在别处硬编码版本号。
- 新增依赖要写清它替代了什么，以及为什么不能自己写或不用。

## 测试

- 命令组的行为用测试覆盖，不依赖真实网络。
- 配置相关的测试用临时目录，不碰用户真实的配置路径。

## 文档

仓库内容默认公开，文档按职责分流：

| 文件 | 职责 |
| --- | --- |
| `README.md` | 用户手册：命令、配置、运行服务、构建、目录 |
| `AGENTS.md` | 本文件：工作规则 |

规则：

- **不写本机与环境特定信息**：账号名、主机名、本机路径、网络环境、代理与镜像源、
  登录状态、密钥、本地配置。文档描述仓库本身，不描述某台机器或某次环境。
  通用的 PATH 惯例（`~/.config`、`~/.local/share`）不算本机信息。
- 只陈述仓库中已经存在的事实。不写计划、进度、待办清单、设计推演或决策记录。
- 功能先实现，再更新文档；命令、配置键、行为的变更与文档同步。
- 实现取舍写进对应源文件的注释，不写进公开文档。
- 文案与提交信息不使用对话式指代（你 / 我 / 咱们），只陈述代码与事实。
- 文档里的行内代码块与表格不受行长限制，正文每行控制在 40 到 60 个字。

## GitHub 协作

`gh` CLI 操作 Issue 与 PR，不经网页手工编辑：

- **不直接推送 `main`**：一切改动走分支。
- 取任务：`gh issue list` / `gh issue view <n>`；
  新建：`gh issue create -t "<type>: <标题>" -b "<现象、期望、验收条件>"`
- 实现走分支：`git checkout -b <type>/<短描述>`，完成后 `gh pr create`，
  标题符合 Conventional Commits，正文写 `Closes #<n>` —— **不用 `Refs #<n>`**，
  `Refs` 只会留下交叉引用，合并后 issue 仍开着，看板就落后于代码。
- 合并前置：`make check` 全绿，CI 通过。
- 改动属于哪个仓库先想清楚：协议转换、选路、重试归 core；SQL、扣费、页面归 web。

## 提交

使用 Conventional Commits（`<type>(<scope>): <描述>`）。

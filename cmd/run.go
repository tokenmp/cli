package cmd

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/tokenmp/core/config"
	"github.com/tokenmp/core/dialect"
	"github.com/tokenmp/core/server"

	"github.com/tokenmp/cli/internal/configmgr"
	"github.com/tokenmp/cli/internal/logging"
	"github.com/tokenmp/cli/internal/media"
	"github.com/tokenmp/cli/internal/metrics"
	"github.com/tokenmp/cli/internal/pluginmgr"
	"github.com/tokenmp/cli/internal/records"
)

// geminiEndpoint 是 Gemini 端点的路径形状。core 没有导出这个前缀，这里只用于启动
// 信息，core 改路由时这里要跟着改。
const geminiEndpoint = "/v1beta/models/{模型名}:{动作}"

// shutdownNote 是启动信息末尾的提示。
const shutdownNote = "按 Ctrl-C 停止：先停止接收新连接，再给在途请求最多 10 秒结束。"

// recordsFlushTimeout 是停机后把内存队列里剩下的调用记录发完的上限。与在途请求的
// 收尾时间一致：两件事同时收尾，不必给两套等待。做成变量是为了测试能缩短它。
var recordsFlushTimeout = 10 * time.Second

func newRunCmd() *cobra.Command {
	var opts runOptions

	cmd := &cobra.Command{
		Use:   "run",
		Short: "启动转发服务",
		Long: `启动转发服务。

按配置装配渠道与插件，然后开始监听，直到收到中断信号。Ctrl-C 与 SIGTERM 之后先
停止接收新连接，再给在途请求最多 10 秒结束，超时就断开。

启动前做三件事：加载并校验配置，配置不合法就直接退出；按 log 段构造服务日志，
写标准错误；构造渠道，凭据环境变量没有设置的渠道在这里就报错，不会拖到请求打过去。

配置里启用但插件目录里找不到的插件会让启动失败，与 core 的行为一致。

每个请求记三行：请求进入、请求完成，以及一行账目与指标。状态码小于 400 记 INFO，
其余记 WARN；默认级别是 info，因此服务在跑就能看见全部请求，包括 /healthz 与未知
路径。一次请求的几条日志共用一个 request_id。

账目与指标行里除了上游给出的 token 数，还记流式请求的首字节延迟 ttft 与生成速率
tps。tps 的分母是生成时长（总时长减 ttft），预填充与排队不算进去；非流式响应一次
写完，这两个数对它没有意义，因此不记。

配置里写了 records 段时，每个请求结束还会把这条账目推给 web 的摄入端点：内存里攒批，
攒够一批或到兜底间隔就发，发失败按退避重试，不挡转发。队列有上限，满了丢最旧的一条
并记 WARN；Ctrl-C 之后先把队列里剩下的发完。端点不通只影响推送，不影响转发。

服务提供的端点：
 - /v1/chat/completions，OpenAI Chat
 - /v1/responses，OpenAI Responses
 - /v1/messages，Claude Messages
 - /v1beta/models/{模型名}:{动作}，Gemini，动作是 generateContent 或 streamGenerateContent
 - /healthz 与 /v1/models

跨协议转换遇到 data URL 形式的图片时能自己解析。按 URL 取回图片内容的能力还没有
实现，遇到这类请求会明确报错，不会静默丢掉图片。`,
		Example: `  # 用当前目录的 config.yaml 启动
  tokenmp run

  # 换一份配置与监听地址
  tokenmp run --config /etc/tokenmp/config.yaml --listen 127.0.0.1:9000

  # 只做装配与预检，不监听
  tokenmp run --dry-run`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runServer(cmd, opts)
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&opts.configPath, "config", "config.yaml", "配置文件路径")
	flags.StringVar(&opts.listen, "listen", "", "覆盖配置里的监听地址，例如 127.0.0.1:9000")
	flags.BoolVar(&opts.dryRun, "dry-run", false, "只做装配与预检，不监听")

	return cmd
}

// runOptions 是 run 命令的参数。
type runOptions struct {
	// configPath 是配置文件路径。
	configPath string
	// listen 覆盖配置里的监听地址，为空时不覆盖。
	listen string
	// dryRun 只做装配与预检。
	dryRun bool
}

// assembly 是一次装配的结果：加载好的配置、构造好的日志与建好的服务。
//
// 做成结构体而不是一串返回值：run 与 test 需要的字段不一样，逐个列出来调用方
// 只会把不关心的值写成下划线。
type assembly struct {
	// Env 是加载与校验过的配置上下文。
	Env *configmgr.Env
	// Config 是生效配置，已填默认值。
	Config *config.Config
	// Loggers 是构造好的日志入口。
	Loggers logging.Loggers
	// Service 是装配好的服务，已经挂上请求结束回调。
	Service *server.Server
}

// assemble 按配置装配服务：加载并校验配置、按 log 段构造日志、注册媒体解析、
// 构造服务并挂上请求结束回调。run 与 test 共用这一条流程。
//
// newLogs 按配置里的 log 段构造日志。两个命令对 core 日志的级别要求不同：run 用
// 配置里的级别，test 默认压到 warn。onDone 在每次请求结束时调用，拿到本次装配的
// 日志入口与 core 交出的原始量；run 用它写账目行，test 用它收集结果。
func assemble(
	configPath string,
	stderr io.Writer,
	newLogs func(config.Log, io.Writer) logging.Loggers,
	onDone func(logging.Loggers, server.Outcome),
) (*assembly, error) {
	// 装配面向运行时：顶层账号展开之后的渠道与规则才是服务实际会用的那份。
	env, err := configmgr.LoadExpanded(configPath)
	if err != nil {
		return nil, err
	}
	cfg := env.Config
	logs := newLogs(cfg.Log, stderr)
	// 媒体解析是进程级设置，启动阶段注册一次。
	media.Register()

	service, err := server.FromConfigWith(cfg, logs.Core, server.WithRequestDone(func(_ context.Context, outcome server.Outcome) {
		onDone(logs, outcome)
	}))
	if err != nil {
		return nil, err
	}
	return &assembly{Env: env, Config: cfg, Loggers: logs, Service: service}, nil
}

// logOutcome 把 core 交出的原始量按 internal/metrics 的口径写成账目行。
func logOutcome(logs logging.Loggers, outcome server.Outcome) {
	if fields := metrics.Fields(outcome); fields != nil {
		logs.Own.Info(metrics.Message(), fields...)
	}
}

// runServer 装配并运行服务。
func runServer(cmd *cobra.Command, opts runOptions) error {
	// 日志与记录推送接在同一处 OnRequestDone 上，共用一份 Outcome：core 只交一次，
	// 两边各拿各的字段，不重复解析。sender 要等配置加载完才知道要不要建，因此先在
	// 闭包里捕获变量，装配之后再赋值。
	var sender *records.Sender
	onDone := func(logs logging.Loggers, outcome server.Outcome) {
		logOutcome(logs, outcome)
		if sender == nil {
			return
		}
		// 只推四种方言入口的调用。/healthz、/v1/models 与未知路径也会走 OnRequestDone，
		// 但它们不是模型调用，进请求日志只会变成噪声。
		if row := records.FromOutcome(outcome, time.Now()); row.Entry != "" {
			sender.Add(row)
		}
	}

	assembled, err := assemble(opts.configPath, cmd.ErrOrStderr(), logging.New, onDone)
	if err != nil {
		return err
	}
	if opts.listen != "" {
		assembled.Config.Listen = opts.listen
	}

	if recordsCfg := assembled.Env.Records; recordsCfg != nil {
		// 密钥在启动时读一次，与渠道凭据同一规矩：缺值就退出，不拖到发不出去才发现。
		secret, err := recordsCfg.SecretFromEnv()
		if err != nil {
			return err
		}
		if !opts.dryRun {
			sender = records.New(*recordsCfg, secret, assembled.Loggers.Own)
			// Serve 返回时转发已经停了，这时再把队列里剩下的发完。
			defer sender.Close(recordsFlushTimeout)
		}
	}

	writer := out(cmd)
	if opts.dryRun {
		printRunSummary(writer, assembled.Env, assembled.Config, assembled.Config.Listen)
		fmt.Fprintf(writer, "\n%s\n", palette.label.Render("--dry-run 只做装配与预检，没有开始监听"))
		return nil
	}

	// 先建监听器再打印，这样 127.0.0.1:0 这类写法报出来的是真实端口。
	listener, err := assembled.Service.Listen(cmd.Context())
	if err != nil {
		return err
	}
	printRunSummary(writer, assembled.Env, assembled.Config, listener.Addr().String())
	fmt.Fprintf(writer, "\n%s\n", palette.label.Render(shutdownNote))

	if err := assembled.Service.Serve(cmd.Context(), listener); err != nil {
		return err
	}
	fmt.Fprintf(writer, "\n%s\n", palette.label.Render("服务已停止"))
	return nil
}

// printRunSummary 打印启动信息：配置、监听地址、规模与端点。
func printRunSummary(w io.Writer, env *configmgr.Env, cfg *config.Config, address string) {
	// 插件目录的判定与插件命令共用一处实现，这里只借用它的 Exists。
	plugins := &pluginmgr.Env{Env: env, Dir: cfg.Plugins.Dir}

	fields := [][2]string{
		{"配置", env.ConfigPath},
		{"监听", address},
		{"日志", cfg.Log.Format + " / " + cfg.Log.Level},
		{"渠道", fmt.Sprintf("%d 个", len(cfg.Channels))},
		{"模型规则", fmt.Sprintf("%d 条", len(cfg.Models))},
		{"插件", fmt.Sprintf("%d 个启用", len(cfg.EnabledPlugins()))},
		{"插件目录", pluginDirText(plugins)},
	}
	if env.Records.Enabled() {
		fields = append(fields, [2]string{"记录推送", env.Records.Endpoint})
	}
	fields = append(fields, [2]string{"端点", endpointList()})

	fmt.Fprintln(w, palette.program.Render("TokenMP 转发服务"))
	printFields(w, fields)
}

// pluginDirText 描述插件目录，目录不存在时标出来。
func pluginDirText(env *pluginmgr.Env) string {
	if env.Exists() {
		return env.Dir
	}
	return env.Dir + "（不存在）"
}

// endpointList 列出服务对外提供的端点。
func endpointList() string {
	return joinList([]string{
		dialect.OpenAIChat.LocalPath(),
		dialect.OpenAIResponses.LocalPath(),
		dialect.ClaudeMessages.LocalPath(),
		geminiEndpoint,
		"/healthz",
		"/v1/models",
	})
}

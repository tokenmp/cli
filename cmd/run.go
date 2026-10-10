package cmd

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/tokenmp/core/config"
	"github.com/tokenmp/core/dialect"
	"github.com/tokenmp/core/server"

	"github.com/tokenmp/cli/internal/configmgr"
	"github.com/tokenmp/cli/internal/logging"
	"github.com/tokenmp/cli/internal/media"
	"github.com/tokenmp/cli/internal/metrics"
	"github.com/tokenmp/cli/internal/pluginmgr"
)

// geminiEndpoint 是 Gemini 端点的路径形状。core 没有导出这个前缀，这里只用于启动
// 信息，core 改路由时这里要跟着改。
const geminiEndpoint = "/v1beta/models/{模型名}:{动作}"

// shutdownNote 是启动信息末尾的提示。
const shutdownNote = "按 Ctrl-C 停止：先停止接收新连接，再给在途请求最多 10 秒结束。"

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

// runServer 装配并运行服务。
func runServer(cmd *cobra.Command, opts runOptions) error {
	env, err := configmgr.Load(opts.configPath)
	if err != nil {
		return err
	}
	cfg := env.Config
	if opts.listen != "" {
		cfg.Listen = opts.listen
	}

	logs := logging.New(cfg.Log, cmd.ErrOrStderr())
	// 媒体解析是进程级设置，启动阶段注册一次。
	media.Register()

	service, err := server.FromConfigWith(cfg, logs.Core, server.WithRequestDone(func(outcome server.Outcome) {
		// 账目与指标由这里写：core 只交原始量，口径定在 internal/metrics。
		if fields := metrics.Fields(outcome); fields != nil {
			logs.Own.Info(metrics.Message(), fields...)
		}
	}))
	if err != nil {
		return err
	}

	writer := out(cmd)
	if opts.dryRun {
		printRunSummary(writer, env, cfg, cfg.Listen)
		fmt.Fprintf(writer, "\n%s\n", palette.label.Render("--dry-run 只做装配与预检，没有开始监听"))
		return nil
	}

	// 先建监听器再打印，这样 127.0.0.1:0 这类写法报出来的是真实端口。
	listener, err := service.Listen(cmd.Context())
	if err != nil {
		return err
	}
	printRunSummary(writer, env, cfg, listener.Addr().String())
	fmt.Fprintf(writer, "\n%s\n", palette.label.Render(shutdownNote))

	if err := service.Serve(cmd.Context(), listener); err != nil {
		return err
	}
	fmt.Fprintf(writer, "\n%s\n", palette.label.Render("服务已停止"))
	return nil
}

// printRunSummary 打印启动信息：配置、监听地址、规模与端点。
func printRunSummary(w io.Writer, env *configmgr.Env, cfg *config.Config, address string) {
	// 插件目录的判定与插件命令共用一处实现，这里只借用它的 Exists。
	plugins := &pluginmgr.Env{Env: env, Dir: cfg.Plugins.Dir}

	fmt.Fprintln(w, palette.program.Render("TokenMP 转发服务"))
	printFields(w, [][2]string{
		{"配置", env.ConfigPath},
		{"监听", address},
		{"日志", cfg.Log.Format + " / " + cfg.Log.Level},
		{"渠道", fmt.Sprintf("%d 个", len(cfg.Channels))},
		{"模型规则", fmt.Sprintf("%d 条", len(cfg.Models))},
		{"插件", fmt.Sprintf("%d 个启用", len(cfg.EnabledPlugins()))},
		{"插件目录", pluginDirText(plugins)},
		{"端点", endpointList()},
	})
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

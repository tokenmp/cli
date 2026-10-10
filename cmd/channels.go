package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/tokenmp/core/config"

	"github.com/tokenmp/cli/internal/configmgr"
)

// channelsOptions 是 channels 命令组共用的选项。
type channelsOptions struct {
	// configPath 是配置文件路径。
	configPath string
	// jsonOut 报告是否给了 --json。非终端时按 JSON 输出，进交互时报错。
	jsonOut bool
}

// newChannelsCmd 组装渠道管理命令组。
func newChannelsCmd() *cobra.Command {
	opts := &channelsOptions{}

	cmd := &cobra.Command{
		Use:   "channels",
		Short: "管理上游渠道",
		Long: `管理上游渠道。

渠道是上游服务的接入点，基址、方言、鉴权、超时与重试都记在同一份配置的 channels 段里。
本组命令只读写配置，改动在服务重启后生效。

list 只报告凭据所在的环境变量有没有设置，不读也不打印它的值。
create 追加前按渠道规则校验，base_url 带了路径却没给 path 这类问题在这里就拦下。
delete 拒绝删除仍被 models 段引用的渠道，并列出引用它的规则。

不带子命令时进入交互界面：

- ↑/↓ 或 j/k 移动，PgUp/PgDn 翻页，Home/End 跳到首尾
- d 删除选中渠道，删除前确认，被模型规则引用时拒绝并指出是哪几条
- q 或 Ctrl-C 退出

输出不是终端时打印与 channels list 相同的表格，不做交互。--json 只在非终端时生效，
进交互时报错。

凭据一律只写环境变量名，配置文件里不出现密钥。`,
		Example: `  # 看现有渠道与凭据状态
  tokenmp channels list

  # 加一个走 OpenAI 协议的渠道
  tokenmp channels create --name openrouter --dialect openai --base-url https://openrouter.ai/api/v1 --path /chat/completions --key-env OPENROUTER_API_KEY

  # 删掉一条渠道，仍被模型规则引用时会报错
  tokenmp channels delete openrouter`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runChannels(cmd, opts, isTerminal(cmd))
		},
	}

	cmd.PersistentFlags().StringVar(&opts.configPath, "config", "config.yaml", "配置文件路径")
	cmd.Flags().BoolVar(&opts.jsonOut, "json", false, "以 JSON 输出，非终端时等价于 channels list --json")

	cmd.AddCommand(
		newChannelsListCmd(opts),
		newChannelsCreateCmd(opts),
		newChannelsDeleteCmd(opts),
	)

	return cmd
}

// runChannels 执行 channels 组不带子命令时的行为。
//
// interactive 报告输出是不是终端。抽成参数后测试可以直接走两个分支，
// 不必让测试进程拥有真的终端。
func runChannels(cmd *cobra.Command, opts *channelsOptions, interactive bool) error {
	// 不是终端时与 channels list 完全一致，脚本与管道拿到的是同一份输出。
	if !interactive {
		env, err := configmgr.Load(opts.configPath)
		if err != nil {
			return err
		}
		if opts.jsonOut {
			return printJSON(cmd, channelRows(env.Config))
		}
		return printChannelsList(cmd, env.Config)
	}

	if opts.jsonOut {
		return errors.New("--json 与交互模式不能同时使用：去掉 --json，或把输出重定向到文件")
	}
	return runBrowse(cmd, channelBrowseList(opts.configPath))
}

// channelRow 是一个渠道的一行摘要，也是 --json 的输出形状。
type channelRow struct {
	Name       string `json:"name"`
	Dialect    string `json:"dialect"`
	BaseURL    string `json:"baseURL"`
	Path       string `json:"path"`
	Auth       string `json:"auth"`
	Credential string `json:"credential"`
	Timeout    string `json:"timeout"`
	MaxRetries int    `json:"maxRetries"`
	Headers    int    `json:"headers,omitempty"`
}

// channelEntry 是写回配置的渠道条目。
//
// 空的可选字段不写出去：path 为空表示按方言取默认路径，max_retries 为空表示不重试，
// 写出 path: "" 这样的空值只会让配置文件变吵。
type channelEntry struct {
	Name       string            `yaml:"name"`
	Dialect    string            `yaml:"dialect"`
	BaseURL    string            `yaml:"base_url"`
	Path       string            `yaml:"path,omitempty"`
	Auth       authEntry         `yaml:"auth"`
	Timeout    config.Duration   `yaml:"timeout,omitempty"`
	MaxRetries *int              `yaml:"max_retries,omitempty"`
	Headers    map[string]string `yaml:"headers,omitempty"`
}

// authEntry 是写回配置的鉴权段。
type authEntry struct {
	Style  string `yaml:"style,omitempty"`
	KeyEnv string `yaml:"key_env,omitempty"`
	Header string `yaml:"header,omitempty"`
	Query  string `yaml:"query,omitempty"`
}

// channelEntryOf 把生效配置里的渠道还原成写回形状，可选字段为空时不带出来。
func channelEntryOf(record config.Channel) channelEntry {
	entry := channelEntry{
		Name:       record.Name,
		Dialect:    record.Dialect,
		BaseURL:    record.BaseURL,
		Path:       record.Path,
		Auth:       authEntry{Style: record.Auth.Style, KeyEnv: record.Auth.KeyEnv, Header: record.Auth.Header, Query: record.Auth.Query},
		Timeout:    record.Timeout,
		MaxRetries: record.MaxRetries,
		Headers:    record.Headers,
	}
	return entry
}

// channelRows 把渠道整理成待输出的形状。
func channelRows(cfg *config.Config) []channelRow {
	rows := make([]channelRow, 0, len(cfg.Channels))
	for _, channel := range cfg.Channels {
		rows = append(rows, channelRow{
			Name:       channel.Name,
			Dialect:    channel.Dialect,
			BaseURL:    channel.BaseURL,
			Path:       channel.Path,
			Auth:       authLabel(channel.Auth),
			Credential: credentialLabel(channel.Auth),
			Timeout:    channel.Timeout.String(),
			MaxRetries: retriesOf(channel),
			Headers:    len(channel.Headers),
		})
	}
	return rows
}

// 凭据状态的三种取值。
const (
	credentialMissing  = "未设置"
	credentialPresent  = "已设置"
	credentialNotNeed  = "不需要"
	credentialNoEnvVar = "缺环境变量名"
)

// authLabel 描述鉴权方式。只出现环境变量名，不出现它的值。
func authLabel(auth config.Auth) string {
	switch auth.Style {
	case "bearer":
		return "bearer ← " + auth.KeyEnv
	case "x-api-key":
		return "x-api-key ← " + auth.KeyEnv
	case "query":
		return fmt.Sprintf("query %s ← %s", auth.Query, auth.KeyEnv)
	case "custom":
		return fmt.Sprintf("请求头 %s ← %s", auth.Header, auth.KeyEnv)
	default:
		return "无"
	}
}

// credentialLabel 只报告凭据所在的环境变量有没有设置，不读也不打印它的值。
func credentialLabel(auth config.Auth) string {
	if auth.Style == "" || auth.Style == "none" {
		return credentialNotNeed
	}
	if auth.KeyEnv == "" {
		return credentialNoEnvVar
	}
	if _, ok := os.LookupEnv(auth.KeyEnv); ok {
		return credentialPresent
	}
	return credentialMissing
}

// credentialText 给凭据状态上色。
func credentialText(state string) string {
	switch state {
	case credentialPresent, credentialNotNeed:
		return palette.ok.Render(state)
	case credentialNoEnvVar:
		return palette.fail.Render(state)
	default:
		return palette.warn.Render(state)
	}
}

// retriesOf 取重试次数，未设置时按 0 处理。
func retriesOf(channel config.Channel) int {
	if channel.MaxRetries == nil {
		return 0
	}
	return *channel.MaxRetries
}

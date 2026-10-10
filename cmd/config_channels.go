package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/tokenmp/core/config"

	"github.com/tokenmp/cli/internal/configmgr"
)

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

func newConfigChannelsCmd(opts *configOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "channels",
		Short: "列出配置里的渠道",
		Long: `列出配置里的渠道。

凭据只显示环境变量名，本命令不读取也不打印它的值。
凭据一列报告那个环境变量有没有设置，好让缺少凭据的渠道在启动前就暴露出来。

path 为空表示按方言取默认路径，timeout 与 max_retries 是填过默认值之后的取值。`,
		Example: `  tokenmp config channels
  tokenmp config channels --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			env, err := configmgr.Load(opts.configPath)
			if err != nil {
				return err
			}

			rows := channelRows(env.Config)

			if opts.json {
				return printJSON(cmd, rows)
			}

			writer := out(cmd)
			if len(rows) == 0 {
				fmt.Fprintln(writer, "配置里没有渠道")
				return nil
			}

			renderer := newTable().Headers("渠道", "方言", "基址", "路径", "鉴权", "凭据", "超时", "重试")
			var missing []string
			for _, row := range rows {
				if row.Credential == credentialMissing {
					missing = append(missing, row.Name)
				}
				renderer.Row(row.Name, row.Dialect, row.BaseURL, dash(row.Path), row.Auth, credentialText(row.Credential), row.Timeout, fmt.Sprintf("%d", row.MaxRetries))
			}
			fmt.Fprintln(writer, renderer.Render())

			if len(missing) > 0 {
				fmt.Fprintf(writer, "\n%s%s\n", palette.warn.Render("凭据环境变量没有设置："), joinList(missing))
			}
			return nil
		},
	}
}

// channelRows 把渠道整理成待输出的形状。config show 的概览视图复用同一份整理结果。
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

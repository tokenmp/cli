package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tokenmp/cli/internal/configmgr"
)

func newChannelsListCmd(opts *channelsOptions) *cobra.Command {
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "list",
		Short: "列出配置里的渠道",
		Long: `列出配置里的渠道。

凭据只显示环境变量名，本命令不读取也不打印它的值。
凭据一列报告那个环境变量有没有设置，好让缺少凭据的渠道在启动前就暴露出来。

path 为空表示按方言取默认路径，timeout 与 max_retries 是填过默认值之后的取值。`,
		Example: `  # 看渠道表
  tokenmp channels list

  # 换一份配置
  tokenmp channels list --config /etc/tokenmp/config.yaml

  # 给脚本用
  tokenmp channels list --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			env, err := configmgr.Load(opts.configPath)
			if err != nil {
				return err
			}

			rows := channelRows(env.Config)

			if jsonOut {
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

	cmd.Flags().BoolVar(&jsonOut, "json", false, "以 JSON 输出，便于脚本处理")
	return cmd
}

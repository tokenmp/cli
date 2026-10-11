package cmd

import (
	"github.com/spf13/cobra"

	"github.com/tokenmp/cli/internal/configmgr"
)

func newAccountsListCmd(opts *accountsOptions) *cobra.Command {
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "list",
		Short: "列出配置里的顶层账号",
		Long: `列出配置里的顶层账号。

凭据只显示环境变量名，本命令不读取也不打印它的值，只报告那个环境变量有没有设置。
「引用渠道」一列是按文件原样读到的、在 auth.accounts 里引用了这个账号的渠道，顺序与
配置一致；顶层账号没有渠道引用时，加载成服务会失败，这里会照样列出来好让账号能被接上。

账号名与下标都是配置文件里的写法，与 config get accounts.0 的口径一致；
展开出来的渠道名（pool/team-a）只用于运行时，这里不出现。`,
		Example: `  # 看账号表
  tokenmp accounts list

  # 换一份配置
  tokenmp accounts list --config /etc/tokenmp/config.yaml

  # 给脚本用
  tokenmp accounts list --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			env, err := configmgr.Load(opts.configPath)
			if err != nil {
				return err
			}

			if jsonOut {
				return printJSON(cmd, accountRows(env.Config))
			}
			return printAccountsList(cmd, env.Config)
		},
	}

	cmd.Flags().BoolVar(&jsonOut, "json", false, "以 JSON 输出，便于脚本处理")
	return cmd
}

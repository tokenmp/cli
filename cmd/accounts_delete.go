package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tokenmp/core/config"

	"github.com/tokenmp/cli/internal/configedit"
	"github.com/tokenmp/cli/internal/configmgr"
)

func newAccountsDeleteCmd(opts *accountsOptions) *cobra.Command {
	var dryRun bool

	cmd := &cobra.Command{
		Use:     "delete <名字>",
		Aliases: []string{"remove"},
		Short:   "删除一个顶层账号",
		Long: `删除一个顶层账号。

仍被渠道在 auth.accounts 里引用的账号会被拒绝删除，并列出引用它的渠道与它们在
channels 段里的下标：删掉账号会让那些引用悬空，配置随即不合法。先把这些渠道的
auth.accounts 里的账号名删掉，再回来删账号。

删到最后一个账号时写出 accounts: []，不留下一个悬空的键。
写回保留注释与缩进，写回后重新加载校验，配置不合法就还原原文件。

remove 是 delete 的别名，用法相同。`,
		Example: `  # 删掉一个不再使用的账号
  tokenmp accounts delete team-c

  # 先看将会删掉哪一条
  tokenmp accounts delete team-c --dry-run

  # 仍被渠道引用时会报错
  tokenmp accounts delete team-a`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]

			env, err := configmgr.Load(opts.configPath)
			if err != nil {
				return err
			}
			account, ok := accountByName(env.Config, name)
			if !ok {
				return fmt.Errorf("配置里没有账号 %s", name)
			}
			index := accountIndex(env.Config, name)
			if err := checkAccountRemovable(env.Config, name); err != nil {
				return err
			}

			if dryRun {
				return printAccountEntry(cmd, env.ConfigPath, "将要删除的账号", accountEntryOf(account))
			}

			if _, err := removeAccountAt(env, index); err != nil {
				return err
			}

			fmt.Fprintf(out(cmd), "%s %s %s\n",
				palette.program.Render(name),
				palette.ok.Render(fmt.Sprintf("已删除（accounts.%d）", index)),
				palette.label.Render("("+env.ConfigPath+")"),
			)
			return nil
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "只打印将要删除的账号，不落盘")
	return cmd
}

// checkAccountRemovable 报告账号能否删除，仍被渠道引用时报错并列出引用的渠道与下标。
func checkAccountRemovable(cfg *config.Config, name string) error {
	refs := referencingChannels(cfg, name)
	if len(refs) == 0 {
		return nil
	}
	return fmt.Errorf("账号 %s 仍被 %d 条渠道引用（%s），先删掉这些渠道 auth.accounts 里的 %s 再试",
		name, len(refs), joinList(channelRefLabels(refs)), name)
}

// removeAccountAt 检查引用后按下标删除账号，返回删掉的账号名。
//
// accounts delete 与交互界面共用这一条路径，两边的判断不会分叉。
func removeAccountAt(env *configmgr.Env, index int) (string, error) {
	if index < 0 || index >= len(env.Config.Accounts) {
		return "", fmt.Errorf("账号下标 %d 越界，共 %d 条", index, len(env.Config.Accounts))
	}
	account := env.Config.Accounts[index]
	if err := checkAccountRemovable(env.Config, account.Name); err != nil {
		return "", err
	}
	if err := configedit.RemoveIndex(env.ConfigPath, []string{"accounts"}, index, reloadableCheck(env.ConfigPath)); err != nil {
		return "", err
	}
	return account.Name, nil
}

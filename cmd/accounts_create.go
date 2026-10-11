package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tokenmp/cli/internal/configedit"
	"github.com/tokenmp/cli/internal/configmgr"
)

func newAccountsCreateCmd(opts *accountsOptions) *cobra.Command {
	var (
		name    string
		keyEnv  string
		headers []string
		dryRun  bool
	)

	cmd := &cobra.Command{
		Use:   "create",
		Short: "追加一个顶层账号",
		Long: `追加一个顶层账号。

账号名重复时报错，不覆盖已有的条目。追加到配置的顶层 accounts 段，渠道再用
auth.accounts 按名字引用它；写回保留注释与缩进，写回后重新加载校验，配置不合法就
还原原文件，与 config set 同一套。

--header 可以重复，写 k=v，进账号级请求头，用于上游按 workspace 或 organization
隔离。账号的凭据只写环境变量名，值由你自己设进环境变量。

写回之后如果这个账号还没有被任何渠道引用，core 会认为配置不合法；
channels create --accounts 接上引用之后即可加载。`,
		Example: `  # 加一个账号
  tokenmp accounts create --name team-a --key-env TEAM_A_API_KEY

  # 带一个 workspace 级请求头，可重复
  tokenmp accounts create --name team-c --key-env TEAM_C_API_KEY --header x-workspace=team-c

  # 先看将要写什么
  tokenmp accounts create --name team-c --key-env TEAM_C_API_KEY --dry-run`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			name = strings.TrimSpace(name)
			keyEnv = strings.TrimSpace(keyEnv)
			if name == "" {
				return errors.New("必须给出 --name")
			}
			if keyEnv == "" {
				return errors.New("必须给出 --key-env")
			}
			headerMap, err := parseHeaderFlags(headers)
			if err != nil {
				return err
			}

			env, err := configmgr.LoadAccounts(opts.configPath)
			if err != nil {
				return err
			}
			if _, exists := accountByName(env.Config, name); exists {
				return fmt.Errorf("账号 %s 已存在，不能覆盖", name)
			}

			entry := accountEntry{Name: name, KeyEnv: keyEnv, Headers: headerMap}

			if dryRun {
				return printAccountEntry(cmd, env.ConfigPath, "将要追加的账号", entry)
			}

			if err := configedit.AppendMapping(env.ConfigPath, []string{"accounts"}, entry, accountsReloadableCheck(env.ConfigPath)); err != nil {
				return err
			}

			fmt.Fprintf(out(cmd), "%s %s %s\n",
				palette.program.Render(name),
				palette.ok.Render("已追加"),
				palette.label.Render("("+env.ConfigPath+")"),
			)
			return nil
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&name, "name", "", "账号名，必须唯一")
	flags.StringVar(&keyEnv, "key-env", "", "凭据所在的环境变量名")
	flags.StringArrayVar(&headers, "header", nil, "账号级请求头，写 k=v，可重复")
	flags.BoolVar(&dryRun, "dry-run", false, "只打印将要写入的条目，不落盘")

	return cmd
}

// printAccountEntry 打印一个账号条目与它要写入的文件，用于 --dry-run 与写回前确认。
func printAccountEntry(cmd *cobra.Command, path, title string, entry accountEntry) error {
	data, err := marshalEntry(entry)
	if err != nil {
		return fmt.Errorf("编码账号条目失败: %w", err)
	}

	writer := out(cmd)
	fmt.Fprintf(writer, "%s %s\n\n", palette.title.Render(title), palette.label.Render("("+path+")"))
	fmt.Fprint(writer, string(data))
	return nil
}

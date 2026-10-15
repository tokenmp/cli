package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tokenmp/core/config"

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
渠道在 auth.accounts 里引用顶层账号时，鉴权一列写出账号名（accounts: team-a），
凭据状态按引用的这些账号的 key_env 判断：全部设好才算已设置。

path 为空表示按方言取默认路径，timeout 与 max_retries 是填过默认值之后的取值。

列出的渠道名与 models 的序号都是配置文件里的写法，与 config get 的口径一致；
顶层账号展开出来的 pool/team-a 只用于运行时，这里是文件里的 pool。`,
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

			if jsonOut {
				return printJSON(cmd, channelRows(env.Config))
			}
			return printChannelsList(cmd, env.Config)
		},
	}

	cmd.Flags().BoolVar(&jsonOut, "json", false, "以 JSON 输出，便于脚本处理")
	return cmd
}

// channelTable 把渠道整理成表格形态，channels list 与交互浏览共用这一份单元格。
//
// 缺凭据的渠道收进表下的说明，只点名这批显示行里的那些。
func channelTable(cfg *config.Config) browseTable {
	rows := channelRows(cfg)
	table := browseTable{
		headers: []string{"渠道", "方言", "基址", "路径", "鉴权", "凭据", "超时", "重试"},
		empty:   "配置里没有渠道",
	}
	for _, row := range rows {
		table.rows = append(table.rows, []string{
			row.Name, row.Dialect, row.BaseURL, dash(row.Path), row.Auth,
			credentialText(row.Credential), row.Timeout, fmt.Sprintf("%d", row.MaxRetries),
		})
	}
	table.note = missingCredentialNote(credentialEntries(rows))
	return table
}

// credentialEntry 是表下说明要用的两项：条目名与它的凭据状态。
//
// 状态取未上色的原值：单元格里那份是上好色的，按它比较既会漏掉颜色差异，
// 也不该让说明的有无取决于终端支不支持颜色。
type credentialEntry struct {
	name  string
	state string
}

// credentialEntries 从渠道行里取出说明要用的名字与凭据状态。
func credentialEntries(rows []channelRow) []credentialEntry {
	entries := make([]credentialEntry, 0, len(rows))
	for _, row := range rows {
		entries = append(entries, credentialEntry{name: row.Name, state: row.Credential})
	}
	return entries
}

// missingCredentialNote 生成表下那行「凭据环境变量没有设置」，只点名显示的那些条目。
func missingCredentialNote(entries []credentialEntry) func(visible []int) string {
	return func(visible []int) string {
		var missing []string
		for _, index := range visible {
			if entries[index].state == credentialMissing {
				missing = append(missing, entries[index].name)
			}
		}
		if len(missing) == 0 {
			return ""
		}
		return palette.warn.Render("凭据环境变量没有设置：") + joinList(missing)
	}
}

// printChannelsList 打印渠道表与缺凭据提示，channels list 与交互模式的非终端退化共用。
func printChannelsList(cmd *cobra.Command, cfg *config.Config) error {
	table := channelTable(cfg)

	writer := out(cmd)
	if len(table.rows) == 0 {
		fmt.Fprintln(writer, table.empty)
		return nil
	}

	fmt.Fprintln(writer, table.render(table.all()))
	if note := table.noteText(table.all()); note != "" {
		fmt.Fprintf(writer, "\n%s\n", note)
	}
	return nil
}

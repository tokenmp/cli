package cmd

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tokenmp/core/config"

	"github.com/tokenmp/cli/internal/configmgr"
)

// accountsOptions 是 accounts 命令组共用的选项。
type accountsOptions struct {
	// configPath 是配置文件路径。
	configPath string
	// jsonOut 报告是否给了 --json。非终端时按 JSON 输出，进交互时报错。
	jsonOut bool
}

// newAccountsCmd 组装顶层账号管理命令组。
func newAccountsCmd() *cobra.Command {
	opts := &accountsOptions{}

	cmd := &cobra.Command{
		Use:   "accounts",
		Short: "管理上游账号",
		Long: `管理上游账号。

账号是顶层的凭据清单，写在配置的 accounts 段里；渠道在 auth.accounts 里按名字引用它，
加载时展开成多条渠道，展开名是「渠道名/账号名」。账号级请求头正是这一层的用处：
上游按 workspace 或 organization 隔离时，同一把凭据要配不同的头。
本组命令只读写配置，改动在服务重启后生效。

list 报告 key_env、凭据状态（已设置 / 未设置）、被哪些渠道引用与账号级请求头的数量。
create 追加到顶层 accounts 段，--header 可重复。
delete 拒绝删除仍被渠道引用的账号，并列出引用它的渠道与它们在 channels 段里的下标。

不带子命令时进入分步管理：先打印与 list 相同的表格，再问做什么。

- 先选新建、编辑、删除或退出；编辑与删除还要从列表里选一条
- 选一项后一个字段一个问题地问下去，编辑时每个字段的默认值是当前值，回车即保留
- 每次只写变化过的字段，写回前打印将要写入的条目并再确认一次
- Esc 或 Ctrl-C 中断当前这件，回到菜单；在菜单上中断则退出，两种情况下都不落盘

输出不是终端时打印与 accounts list 相同的表格，不做交互。--json 只在非终端时生效，
进交互时报错。凭据一律只写环境变量名，配置文件里不出现密钥。`,
		Example: `  # 看现有账号与引用它的渠道
  tokenmp accounts list

  # 加一个带 workspace 头的账号
  tokenmp accounts create --name team-c --key-env TEAM_C_API_KEY --header x-workspace=team-c

  # 删掉一个不再被渠道引用的账号
  tokenmp accounts delete team-c`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAccounts(cmd, opts, isTerminal(cmd))
		},
	}

	cmd.PersistentFlags().StringVar(&opts.configPath, "config", "config.yaml", "配置文件路径")
	cmd.Flags().BoolVar(&opts.jsonOut, "json", false, "以 JSON 输出，非终端时等价于 accounts list --json")

	cmd.AddCommand(
		newAccountsListCmd(opts),
		newAccountsCreateCmd(opts),
		newAccountsDeleteCmd(opts),
	)

	return cmd
}

// runAccounts 执行 accounts 组不带子命令时的行为。
//
// interactive 报告输出是不是终端。抽成参数后测试可以直接走两个分支，
// 不必让测试进程拥有真的终端。
func runAccounts(cmd *cobra.Command, opts *accountsOptions, interactive bool) error {
	// 不是终端时与 accounts list 完全一致，脚本与管道拿到的是同一份输出。
	if !interactive {
		env, err := configmgr.Load(opts.configPath)
		if err != nil {
			return err
		}
		if opts.jsonOut {
			return printJSON(cmd, accountRows(env.Config))
		}
		return printAccountsList(cmd, env.Config)
	}

	if opts.jsonOut {
		return errors.New("--json 与交互模式不能同时使用：去掉 --json，或把输出重定向到文件")
	}
	return runAccountsManage(cmd, opts.configPath)
}

// accountRow 是一个顶层账号的一行摘要，也是 --json 的输出形状。
//
// Channels 是按文件原样读到的、在 auth.accounts 里引用了这个账号的渠道名，顺序与
// channels 段一致；Headers 是账号级请求头的数量。
type accountRow struct {
	Name       string   `json:"name"`
	KeyEnv     string   `json:"keyEnv"`
	Credential string   `json:"credential"`
	Channels   []string `json:"channels"`
	Headers    int      `json:"headers,omitempty"`
}

// accountEntry 是写回顶层 accounts 段的账号条目。
//
// headers 为空时不写出去：账号不需要额外请求头时，写出 headers: {} 只会让配置变吵。
type accountEntry struct {
	Name    string            `yaml:"name"`
	KeyEnv  string            `yaml:"key_env"`
	Headers map[string]string `yaml:"headers,omitempty"`
}

// accountEntryOf 把配置里的账号还原成写回形状，没有请求头时不带出来。
func accountEntryOf(account config.Account) accountEntry {
	return accountEntry{
		Name:    account.Name,
		KeyEnv:  account.KeyEnv,
		Headers: account.Headers,
	}
}

// accountRows 把顶层账号整理成待输出的形状。
func accountRows(cfg *config.Config) []accountRow {
	rows := make([]accountRow, 0, len(cfg.Accounts))
	for _, account := range cfg.Accounts {
		rows = append(rows, accountRow{
			Name:       account.Name,
			KeyEnv:     account.KeyEnv,
			Credential: accountCredentialState(account),
			Channels:   referencingChannelNames(cfg, account.Name),
			Headers:    len(account.Headers),
		})
	}
	return rows
}

// accountCredentialState 只报告账号的 key_env 有没有设置，不读也不打印它的值。
//
// 账号的凭据来源只有 key_env 一种，因此这里不会出现「不需要」。
func accountCredentialState(account config.Account) string {
	if account.KeyEnv == "" {
		return credentialNoEnvVar
	}
	if _, ok := os.LookupEnv(account.KeyEnv); ok {
		return credentialPresent
	}
	return credentialMissing
}

// printAccountsList 打印账号表与缺凭据提示，accounts list 与交互模式的非终端退化共用。
func printAccountsList(cmd *cobra.Command, cfg *config.Config) error {
	rows := accountRows(cfg)

	writer := out(cmd)
	if len(rows) == 0 {
		fmt.Fprintln(writer, "配置里没有顶层账号")
		return nil
	}

	renderer := newTable().Headers("账号", "key_env", "凭据", "引用渠道", "请求头")
	var missing []string
	for _, row := range rows {
		if row.Credential == credentialMissing {
			missing = append(missing, row.Name)
		}
		renderer.Row(row.Name, row.KeyEnv, credentialText(row.Credential), dash(strings.Join(row.Channels, "、")), fmt.Sprintf("%d", row.Headers))
	}
	fmt.Fprintln(writer, renderer.Render())

	if len(missing) > 0 {
		fmt.Fprintf(writer, "\n%s%s\n", palette.warn.Render("凭据环境变量没有设置："), joinList(missing))
	}
	return nil
}

// accountIndex 返回账号在 accounts 段里的下标，调用方已确认它存在。
func accountIndex(cfg *config.Config, name string) int {
	for i, account := range cfg.Accounts {
		if account.Name == name {
			return i
		}
	}
	return -1
}

// accountChannelRef 是一条引用了账号的渠道：文件里的下标与名字。
type accountChannelRef struct {
	index int
	name  string
}

// referencingChannels 列出在 auth.accounts 里引用了指定账号的渠道，顺序与文件一致。
func referencingChannels(cfg *config.Config, name string) []accountChannelRef {
	var refs []accountChannelRef
	for i, channel := range cfg.Channels {
		for _, referenced := range channel.Auth.Accounts {
			if referenced == name {
				refs = append(refs, accountChannelRef{index: i, name: channel.Name})
				break
			}
		}
	}
	return refs
}

// referencingChannelNames 给出引用指定账号的渠道名，给列表用。
func referencingChannelNames(cfg *config.Config, name string) []string {
	refs := referencingChannels(cfg, name)
	names := make([]string, 0, len(refs))
	for _, ref := range refs {
		names = append(names, ref.name)
	}
	return names
}

// channelRefLabels 把引用渠道写成「channels[0]（pool）」这样的定位文字。
func channelRefLabels(refs []accountChannelRef) []string {
	labels := make([]string, 0, len(refs))
	for _, ref := range refs {
		labels = append(labels, fmt.Sprintf("channels[%d]（%s）", ref.index, ref.name))
	}
	return labels
}

// parseHeaderFlags 把可重复的 --header k=v 解析成请求头映射。
//
// 只按第一个 = 切分，值里可以再出现 =。名字留空时报错，值允许留空。
func parseHeaderFlags(pairs []string) (map[string]string, error) {
	if len(pairs) == 0 {
		return nil, nil
	}
	headers := make(map[string]string, len(pairs))
	for _, pair := range pairs {
		name, value, ok := strings.Cut(pair, "=")
		name = strings.TrimSpace(name)
		if !ok || name == "" {
			return nil, fmt.Errorf("请求头 %q 的写法应当是 k=v", pair)
		}
		headers[name] = strings.TrimSpace(value)
	}
	return headers, nil
}

// formatHeaderText 把请求头写成排序后的「k=v」列表，给交互编辑的文本框用。
func formatHeaderText(headers map[string]string) string {
	if len(headers) == 0 {
		return ""
	}
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)
	pairs := make([]string, 0, len(names))
	for _, name := range names {
		pairs = append(pairs, name+"="+headers[name])
	}
	return strings.Join(pairs, ", ")
}

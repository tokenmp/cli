package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tokenmp/cli/internal/pluginmgr"
)

func newPluginListCmd(opts *pluginOptions) *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "列出插件与它们的启用状态",
		Long: `列出插件与它们的启用状态。

顺序即转发时的执行顺序：启用中的插件按 order 升序在前，编译失败的其次，目录里有
但配置没登记的再次，停用的最后。配置里登记了但目录里没有的插件也列出来，不影响
其余部分。单个插件编译失败不影响这份列表，失败原因单独列在表后。`,
		Args: cobra.NoArgs,
		Example: `  # 看插件目录里的全部插件
  tokenmp plugin list

  # 换一份配置
  tokenmp plugin list --config /etc/tokenmp/config.yaml

  # 给脚本用
  tokenmp plugin list --json`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			env, err := pluginmgr.Load(opts.configPath, opts.dir)
			if err != nil {
				return err
			}
			states, err := env.States()
			if err != nil {
				return err
			}

			if opts.json {
				summaries := make([]pluginmgr.Summary, 0, len(states))
				for _, state := range states {
					summaries = append(summaries, state.Summary())
				}
				return printJSON(cmd, summaries)
			}

			writer := out(cmd)
			if len(states) == 0 {
				fmt.Fprintf(writer, "插件目录 %s 里没有插件\n", env.Dir)
				return nil
			}

			renderer := newTable().Headers("插件", "名称", "版本", "钩子", "状态", "顺序")
			var missing, broken []string
			for _, state := range states {
				summary := state.Summary()
				switch {
				case summary.Error != "":
					broken = append(broken, fmt.Sprintf("%s: %s", summary.Key, summary.Error))
				case summary.Missing:
					missing = append(missing, fmt.Sprintf("%s（目录 %s 里没有对应文件）", summary.Key, env.Dir))
				}
				renderer.Row(summary.Key, dash(summary.Name), dash(summary.Version), joinHooks(summary.Hooks), stateLabel(summary), orderLabel(summary))
			}
			fmt.Fprintln(writer, renderer.Render())

			if len(broken) > 0 {
				fmt.Fprintf(writer, "\n%s\n", palette.fail.Render("编译失败的插件："))
				for _, item := range broken {
					fmt.Fprintf(writer, "  %s\n", item)
				}
			}
			if len(missing) > 0 {
				fmt.Fprintf(writer, "\n%s%s\n", palette.fail.Render("配置登记但目录里没有："), joinList(missing))
			}
			return nil
		},
	}
}

// stateLabel 把状态压成一个词。
func stateLabel(summary pluginmgr.Summary) string {
	switch {
	case summary.Error != "":
		return palette.fail.Render("编译失败")
	case summary.Missing:
		return palette.fail.Render("缺失")
	case !summary.Registered:
		return palette.warn.Render("未登记")
	case summary.Enabled:
		return palette.ok.Render("启用")
	default:
		return palette.label.Render("停用")
	}
}

// orderLabel 只在插件参与转发时给出顺序，其余情况显示占位符。
func orderLabel(summary pluginmgr.Summary) string {
	if !summary.Enabled {
		return "-"
	}
	return fmt.Sprintf("%d", summary.Order)
}

// dash 把空字符串换成占位符，避免表格里出现空格子。
func dash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

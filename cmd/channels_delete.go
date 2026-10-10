package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tokenmp/core/config"

	"github.com/tokenmp/cli/internal/configedit"
	"github.com/tokenmp/cli/internal/configmgr"
)

func newChannelsDeleteCmd(opts *channelsOptions) *cobra.Command {
	var dryRun bool

	cmd := &cobra.Command{
		Use:     "delete <名字>",
		Aliases: []string{"remove"},
		Short:   "删除一个渠道",
		Long: `删除一个渠道。

仍被 models 段引用的渠道会被拒绝删除，并列出引用它的规则：删掉渠道会让那些规则悬空，
配置随即不合法。先删除对应的规则，或改用别的渠道。

删到最后一个渠道时写出 channels: []，不留下一个悬空的键。
写回保留注释与缩进，写回后重新加载校验，配置不合法就还原原文件。

remove 是 delete 的别名，用法相同。`,
		Example: `  # 删掉一条不再使用的渠道
  tokenmp channels delete local

  # 先看将会删掉哪一条
  tokenmp channels delete local --dry-run

  # 被模型规则引用时会报错
  tokenmp channels delete openrouter`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]

			env, err := configmgr.Load(opts.configPath)
			if err != nil {
				return err
			}
			record, ok := env.Config.ChannelByName(name)
			if !ok {
				return fmt.Errorf("配置里没有渠道 %s", name)
			}

			index := channelIndex(env.Config, name)
			if err := checkChannelRemovable(env.Config, name); err != nil {
				return err
			}

			if dryRun {
				return printChannelEntry(cmd, env.ConfigPath, "将要删除的渠道", channelEntryOf(record))
			}

			if _, err := removeChannelAt(env, index); err != nil {
				return err
			}

			fmt.Fprintf(out(cmd), "%s %s %s\n",
				palette.program.Render(name),
				palette.ok.Render(fmt.Sprintf("已删除（channels.%d）", index)),
				palette.label.Render("("+env.ConfigPath+")"),
			)
			return nil
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "只打印将要删除的渠道，不落盘")
	return cmd
}

// checkChannelRemovable 报告渠道能否删除，仍被模型规则引用时报错并列出引用的规则。
func checkChannelRemovable(cfg *config.Config, name string) error {
	refs := referencingRules(cfg, name)
	if len(refs) == 0 {
		return nil
	}
	return fmt.Errorf("渠道 %s 仍被 %d 条模型规则引用（%s），先删除这些规则或改用别的渠道",
		name, len(refs), joinList(refs))
}

// removeChannelAt 检查引用后按下标删除渠道，返回删掉的渠道名。
//
// channels delete 与交互界面共用这一条路径，两边的判断不会分叉。
func removeChannelAt(env *configmgr.Env, index int) (string, error) {
	if index < 0 || index >= len(env.Config.Channels) {
		return "", fmt.Errorf("渠道下标 %d 越界，共 %d 条", index, len(env.Config.Channels))
	}
	record := env.Config.Channels[index]
	if err := checkChannelRemovable(env.Config, record.Name); err != nil {
		return "", err
	}
	if err := configedit.RemoveIndex(env.ConfigPath, []string{"channels"}, index, reloadableCheck(env.ConfigPath)); err != nil {
		return "", err
	}
	return record.Name, nil
}

// channelIndex 返回渠道在 channels 段里的下标，调用方已确认它存在。
func channelIndex(cfg *config.Config, name string) int {
	for i, channel := range cfg.Channels {
		if channel.Name == name {
			return i
		}
	}
	return -1
}

// referencingRules 列出引用了指定渠道的模型规则，带上下标与匹配式。
func referencingRules(cfg *config.Config, name string) []string {
	var refs []string
	for i, rule := range cfg.Models {
		if rule.Channel == name {
			refs = append(refs, fmt.Sprintf("models[%d]（match %s）", i, rule.Match))
		}
	}
	return refs
}

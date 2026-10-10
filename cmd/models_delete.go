package cmd

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/tokenmp/core/config"

	"github.com/tokenmp/cli/internal/configedit"
	"github.com/tokenmp/cli/internal/configmgr"
)

func newModelsDeleteCmd(opts *modelsOptions) *cobra.Command {
	var (
		channelName string
		dryRun      bool
	)

	cmd := &cobra.Command{
		Use:     "delete <序号 | 匹配式>",
		Aliases: []string{"remove"},
		Short:   "删除一条模型路由规则",
		Long: `删除一条模型路由规则。

定位有两种写法，序号是 models 段里的下标，与 models list 表里的序号一致：

- 给一个数字，按序号删除，例如 tokenmp models delete 1
- 给一个匹配式，按 match 字段的原样写法删除，可以加 --channel 限定渠道

匹配式对应多条规则时不会猜，会列出候选并要求用 --channel 指定其中一条。
删除后打印删掉的那条的匹配式与渠道，不与剩下的规则混淆。

写回保留注释与缩进，删到一条不剩时写出 models: []，写回后重新加载校验，
配置不合法就还原原文件。

remove 是 delete 的别名，用法相同。`,
		Example: `  # 按序号删除
  tokenmp models delete 1

  # 按匹配式删除，规则有多条时用渠道限定
  tokenmp models delete "deepseek-*" --channel commandcode

  # 先看将会删掉哪一条
  tokenmp models delete 0 --dry-run`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			env, err := configmgr.Load(opts.configPath)
			if err != nil {
				return err
			}

			index, err := resolveModelIndex(env.Config, args[0], channelName)
			if err != nil {
				return err
			}
			rule := env.Config.Models[index]

			if dryRun {
				return printModelEntry(cmd, env.ConfigPath, fmt.Sprintf("将要删除的规则（models.%d）", index), modelEntryOf(rule))
			}

			if _, err := removeModelAt(env, index); err != nil {
				return err
			}

			fmt.Fprintf(out(cmd), "%s → %s %s %s\n",
				palette.program.Render(rule.Match),
				palette.program.Render(rule.Channel),
				palette.ok.Render(fmt.Sprintf("已删除（models.%d）", index)),
				palette.label.Render("("+env.ConfigPath+")"),
			)
			return nil
		},
	}

	cmd.Flags().StringVar(&channelName, "channel", "", "按匹配式定位时限定渠道")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "只打印将要删除的规则，不落盘")
	return cmd
}

// removeModelAt 按下标删除模型规则，返回删掉的规则。
//
// models delete 与交互界面共用这一条路径。
func removeModelAt(env *configmgr.Env, index int) (*config.Model, error) {
	if index < 0 || index >= len(env.Config.Models) {
		return nil, fmt.Errorf("规则下标 %d 越界，共 %d 条", index, len(env.Config.Models))
	}
	rule := env.Config.Models[index]
	if err := configedit.RemoveIndex(env.ConfigPath, []string{"models"}, index, reloadableCheck(env.ConfigPath)); err != nil {
		return nil, err
	}
	return &rule, nil
}

// resolveModelIndex 把序号或匹配式解析成规则下标。
//
// 参数是纯数字时按序号处理，否则按 match 字段的原样写法匹配。匹配式对应多条规则而
// 没给渠道时报错并列出候选，不替调用方猜。
func resolveModelIndex(cfg *config.Config, target, channelName string) (int, error) {
	if index, err := strconv.Atoi(target); err == nil {
		if index < 0 || index >= len(cfg.Models) {
			return -1, fmt.Errorf("序号 %d 越界，共 %d 条规则", index, len(cfg.Models))
		}
		if rule := cfg.Models[index]; channelName != "" && rule.Channel != channelName {
			return -1, fmt.Errorf("序号 %d 指向的渠道是 %s，与 --channel %s 不符", index, rule.Channel, channelName)
		}
		return index, nil
	}

	var matches []int
	for i, rule := range cfg.Models {
		if rule.Match == target && (channelName == "" || rule.Channel == channelName) {
			matches = append(matches, i)
		}
	}

	switch len(matches) {
	case 0:
		if channelName != "" {
			return -1, fmt.Errorf("没有 match 为 %s、渠道为 %s 的规则", target, channelName)
		}
		return -1, fmt.Errorf("没有 match 为 %s 的规则", target)
	case 1:
		return matches[0], nil
	default:
		return -1, fmt.Errorf("match %s 对应 %d 条规则（%s），用 --channel 指定其中一条",
			target, len(matches), joinList(modelRefs(cfg, matches)))
	}
}

// modelRefs 把规则下标写成 models.N → 渠道 的列表，用于报错。
func modelRefs(cfg *config.Config, indexes []int) []string {
	refs := make([]string, 0, len(indexes))
	for _, index := range indexes {
		refs = append(refs, fmt.Sprintf("models.%d → %s", index, cfg.Models[index].Channel))
	}
	return refs
}

package cmd

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tokenmp/core/dialect"

	"github.com/tokenmp/cli/internal/configedit"
	"github.com/tokenmp/cli/internal/configmgr"
)

func newModelsCreateCmd(opts *modelsOptions) *cobra.Command {
	var (
		match       string
		channelName string
		upstream    string
		dialectName string
		affinity    string
		dryRun      bool
	)

	cmd := &cobra.Command{
		Use:   "create",
		Short: "追加一条模型路由规则",
		Long: `追加一条模型路由规则。

规则追加到 models 段末尾，也就是候选顺序的最后。引用了不存在的渠道时报错，不会写出
一条指向空渠道的规则。

同一条 match 与 channel 组合的规则只能有一条，重复时报错，不覆盖。
upstream 里只认 ${model} 占位符，写法不对时写回校验会失败并还原原文件。

写回保留注释与缩进，空的可选字段不会被写出来，例如没给 --upstream 时不会写出
upstream: ""。`,
		Example: `  # 把 deepseek-* 交给 commandcode，上游模型名加一层前缀
  tokenmp models create --match "deepseek-*" --channel commandcode --upstream "deepseek/${model}"

  # 两条渠道组成一个亲和池
  tokenmp models create --match "gpt-*" --channel account-a --affinity accounts

  # 先看将要写什么
  tokenmp models create --match "gpt-*" --channel account-a --dry-run`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if match == "" {
				return errors.New("必须给出 --match")
			}
			if channelName == "" {
				return errors.New("必须给出 --channel")
			}
			if dialectName != "" {
				if _, ok := dialect.Parse(dialectName); !ok {
					return fmt.Errorf("方言 %q 不是支持的取值", dialectName)
				}
			}

			env, err := configmgr.Load(opts.configPath)
			if err != nil {
				return err
			}
			if _, ok := env.Config.ChannelByName(channelName); !ok {
				return fmt.Errorf("渠道 %s 不存在，先建渠道再写规则", channelName)
			}
			for _, rule := range env.Config.Models {
				if rule.Match == match && rule.Channel == channelName {
					return fmt.Errorf("规则 %s → %s 已存在，不能重复添加", match, channelName)
				}
			}

			entry := modelEntry{
				Match:    match,
				Channel:  channelName,
				Upstream: upstream,
				Dialect:  dialectName,
				Affinity: affinity,
			}

			if dryRun {
				return printModelEntry(cmd, env.ConfigPath, "将要追加的规则", entry)
			}

			if err := configedit.AppendMapping(env.ConfigPath, []string{"models"}, entry, reloadableCheck(env.ConfigPath)); err != nil {
				return err
			}

			fmt.Fprintf(out(cmd), "%s → %s %s %s\n",
				palette.program.Render(match),
				palette.program.Render(channelName),
				palette.ok.Render("已追加"),
				palette.label.Render("("+env.ConfigPath+")"),
			)
			return nil
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&match, "match", "", "模型名匹配式，支持 * 与 ? 通配")
	flags.StringVar(&channelName, "channel", "", "命中的请求发往哪个渠道")
	flags.StringVar(&upstream, "upstream", "", "发往上游的模型名，留空表示用请求里的模型名，可写 ${model}")
	flags.StringVar(&dialectName, "dialect", "", "覆盖渠道方言，留空表示用渠道的方言")
	flags.StringVar(&affinity, "affinity", "", "渠道亲和的组名，留空表示不参与")
	flags.BoolVar(&dryRun, "dry-run", false, "只打印将要写入的条目，不落盘")

	return cmd
}

// printModelEntry 打印一条规则与它要写入的文件，用于 --dry-run。
func printModelEntry(cmd *cobra.Command, path, title string, entry modelEntry) error {
	data, err := marshalEntry(entry)
	if err != nil {
		return fmt.Errorf("编码规则条目失败: %w", err)
	}

	writer := out(cmd)
	fmt.Fprintf(writer, "%s %s\n\n", palette.title.Render(title), palette.label.Render("("+path+")"))
	fmt.Fprint(writer, string(data))
	return nil
}

package cmd

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/tokenmp/cli/internal/pluginmgr"
)

func newPluginEnableCmd(opts *pluginOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "enable <key...>",
		Short: "启用插件",
		Long: `启用插件。

把配置 plugins.list 段里对应条目的 enabled 设为 true，条目不存在时补一条。
插件目录里没有这个 key 时报错，不写配置。
写回后重新加载配置校验，校验失败时还原原文件。`,
		Example: `  # 启用一个插件
  tokenmp plugin enable echo

  # 一次启用多个
  tokenmp plugin enable echo html-escape`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return updatePlugins(cmd, opts, args, pluginmgr.FieldEnabled, true, "已启用")
		},
	}
}

func newPluginDisableCmd(opts *pluginOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "disable <key...>",
		Short: "停用插件",
		Long: `停用插件。

把配置 plugins.list 段里对应条目的 enabled 设为 false，条目与插件文件都保留。
停用的插件不参与转发，但仍会出现在 list 与 check 的结果里。`,
		Example: `  # 停用一个插件，配置条目保留
  tokenmp plugin disable echo

  # 一次停用多个
  tokenmp plugin disable echo html-escape`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return updatePlugins(cmd, opts, args, pluginmgr.FieldEnabled, false, "已停用")
		},
	}
}

func newPluginOrderCmd(opts *pluginOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "order <key> <number>",
		Short: "设置插件的执行顺序",
		Long: `设置插件的执行顺序。

order 越小越先执行，order 相同的插件按配置里的先后顺序执行。
改顺序不会改变启用状态，插件停用时顺序仍然记在配置里。`,
		Example: `  # 越小的先执行
  tokenmp plugin order echo 10
  tokenmp plugin order html-escape 20

  # 改的是另一份配置
  tokenmp plugin order echo 10 --config /etc/tokenmp/config.yaml`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			order, err := strconv.Atoi(args[1])
			if err != nil || order < 0 {
				return fmt.Errorf("顺序必须是非负整数，实际是 %q", args[1])
			}
			return updatePlugins(cmd, opts, args[:1], pluginmgr.FieldOrder, order, fmt.Sprintf("顺序已设为 %d", order))
		},
	}
}

// updatePlugins 逐个校验并改写插件的配置条目。
//
// 先确认插件目录里有这个 key：把不存在的插件写进配置，服务启动时才会报错，在这里
// 拦下来能省一次重启。
func updatePlugins(cmd *cobra.Command, opts *pluginOptions, keys []string, field string, value any, done string) error {
	env, err := pluginmgr.Load(opts.configPath, opts.dir)
	if err != nil {
		return err
	}
	if !env.HasConfig {
		return fmt.Errorf("配置文件 %s 不存在，先写一份配置再改插件状态", env.ConfigPath)
	}

	writer := out(cmd)
	for _, key := range keys {
		if _, err := env.Find(key); err != nil {
			return err
		}
		if err := env.Edit(key, field, value); err != nil {
			return err
		}
		fmt.Fprintf(writer, "%s %s %s\n", palette.program.Render(key), done, palette.label.Render("("+env.ConfigPath+")"))
	}
	return nil
}

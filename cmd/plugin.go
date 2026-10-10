package cmd

import (
	"github.com/spf13/cobra"
)

// pluginOptions 是 plugin 命令组共用的选项。
type pluginOptions struct {
	// configPath 是配置文件路径。
	configPath string
	// dir 给出时覆盖配置里的 plugins.dir。
	dir string
	// json 打开机器可读输出。
	json bool
}

// newPluginCmd 组装插件管理命令组。
func newPluginCmd() *cobra.Command {
	opts := &pluginOptions{}

	cmd := &cobra.Command{
		Use:   "plugin",
		Short: "管理本地插件",
		Long: `管理本地插件。

插件是单文件同步 ECMAScript 模块，放在配置 plugins.dir 指向的目录里。是否参与
转发与执行顺序记在同一份配置的 plugins.list 段里。本组命令只读写配置与插件文件，
不接触正在运行的服务，改动在服务重启或重载配置后生效。`,
		Example: `  # 列出插件与状态，顺序即执行顺序
  tokenmp plugin list

  # 检查语法、自述信息与配置登记
  tokenmp plugin check

  # 回放一个插件的 fixture
  tokenmp plugin replay echo

  # 启用插件并调整顺序
  tokenmp plugin enable echo
  tokenmp plugin order echo 10`,
	}

	flags := cmd.PersistentFlags()
	flags.StringVar(&opts.configPath, "config", "config.yaml", "配置文件路径")
	flags.StringVar(&opts.dir, "dir", "", "插件目录，给出时覆盖配置里的 plugins.dir")
	flags.BoolVar(&opts.json, "json", false, "以 JSON 输出，便于脚本处理")

	cmd.AddCommand(
		newPluginListCmd(opts),
		newPluginShowCmd(opts),
		newPluginCheckCmd(opts),
		newPluginReplayCmd(opts),
		newPluginEnableCmd(opts),
		newPluginDisableCmd(opts),
		newPluginOrderCmd(opts),
		newPluginHooksCmd(opts),
		newPluginNewCmd(opts),
	)

	return cmd
}

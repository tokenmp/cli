package cmd

import (
	"github.com/spf13/cobra"
)

// configOptions 是 config 命令组共用的选项。
type configOptions struct {
	// configPath 是配置文件路径。
	configPath string
	// json 打开机器可读输出。
	json bool
}

// newConfigCmd 组装配置命令组。
func newConfigCmd() *cobra.Command {
	opts := &configOptions{}

	cmd := &cobra.Command{
		Use:   "config",
		Short: "查看与校验配置",
		Long: `查看与校验配置。

配置是单个 YAML 文件，解码时开启 KnownFields，拼错的字段在加载时报错。
本组命令读的是同一份文件，相对路径（例如插件目录）以配置文件所在目录为基准。

show 与 get 看到的是填过默认值的生效配置，不是文件原文；
set 会改写文件，注释与缩进保留，写回后重新加载校验，不合法就还原。`,
		Example: `  # 看生效配置
  tokenmp config show

  # 校验，配置不合法时退出码 1
  tokenmp config validate

  # 取一个字段，路径与配置文件同形
  tokenmp config get listen
  tokenmp config get channels.0.base_url

  # 改一个标量字段
  tokenmp config set log.level debug

  # 看渠道与模型路由
  tokenmp config channels
  tokenmp config models --model gpt-4o`,
	}

	flags := cmd.PersistentFlags()
	flags.StringVar(&opts.configPath, "config", "config.yaml", "配置文件路径")
	flags.BoolVar(&opts.json, "json", false, "以 JSON 输出，便于脚本处理")

	cmd.AddCommand(
		newConfigPathCmd(opts),
		newConfigShowCmd(opts),
		newConfigValidateCmd(opts),
		newConfigGetCmd(opts),
		newConfigSetCmd(opts),
		newConfigChannelsCmd(opts),
		newConfigModelsCmd(opts),
		newConfigInitCmd(opts),
	)

	return cmd
}

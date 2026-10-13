package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tokenmp/cli/internal/configmgr"
)

// configHeader 是 config init 生成的骨架顶上那句注释。
const configHeader = "tokenmp 配置。凭据只写环境变量名，不写密钥。"

// configSections 是骨架里顶层标量之后的示例段。
//
// 顶层标量（listen、log、plugins.dir 这些）由 core 的键表与默认值生成，见
// configmgr.Template；这里只写键表管不了的部分：渠道、模型规则与插件清单的条目，
// 它们带下标或随使用者填，不是「整份配置一个值」的键。
//
// 渠道的 base_url 带了 /v1，因此必须显式给出 path，取值从 /v1 之后算起。这条规则
// 由渠道构造把关，config validate 不查，漏掉的话要到 run 启动时才报错。
const configSections = `channels:
  - name: example
    dialect: openai
    base_url: https://api.example.com/v1
    path: /chat/completions
    auth:
      style: bearer
      key_env: EXAMPLE_API_KEY
    timeout: 120s

models:
  - match: "gpt-*"
    channel: example

plugins:
  list: []
`

func newConfigInitCmd(opts *configOptions) *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "init",
		Short: "生成一份配置骨架",
		Long: `生成一份配置骨架。

骨架里的顶层标量按 core 的键表与默认值生成（listen、log、plugins.dir 这些），
其余部分带一个渠道、一条模型规则与一个空的插件清单，通过校验也能直接启动。
改掉渠道基址与凭据环境变量名之后即可起服务。

配置文件已存在时报错，除非给出 --force。
所在目录不存在时会创建。`,
		Example: `  tokenmp config init
  tokenmp config init --config /etc/tokenmp/config.yaml
  tokenmp config init --force`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			env, err := configmgr.Open(opts.configPath)
			if err != nil {
				return err
			}
			if env.HasConfig && !force {
				return fmt.Errorf("%s 已存在，改一个路径或加 --force", env.ConfigPath)
			}
			template, err := configmgr.Template(configHeader, configSections)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(env.Dir, 0o755); err != nil {
				return fmt.Errorf("创建目录 %s 失败: %w", env.Dir, err)
			}
			if err := os.WriteFile(env.ConfigPath, template, 0o644); err != nil {
				return fmt.Errorf("写入 %s 失败: %w", env.ConfigPath, err)
			}

			writer := out(cmd)
			fmt.Fprintf(writer, "%s %s\n\n", palette.ok.Render("已创建"), env.ConfigPath)
			fmt.Fprintln(writer, strings.Join([]string{
				fmt.Sprintf("  tokenmp config validate --config %s", env.ConfigPath),
				fmt.Sprintf("  tokenmp channels list --config %s", env.ConfigPath),
				fmt.Sprintf("  tokenmp plugin list --config %s", env.ConfigPath),
			}, "\n"))
			return nil
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "配置文件已存在时覆盖")
	return cmd
}

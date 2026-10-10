package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tokenmp/cli/internal/configmgr"
)

func newConfigShowCmd(opts *configOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "打印生效配置",
		Long: `打印生效配置。

输出的是填过默认值的配置，因此没写在文件里的字段也会出现，例如 listen、log 与插件目录。
空值字段不打印：空字符串、空段落与空列表只表示没写，没有信息量。
字段顺序与配置文件一致。

第一行是配置文件路径，写成注释，重定向出去就是一份可以直接用的配置。`,
		Example: `  tokenmp config show
  tokenmp config show --json
  tokenmp config show > /tmp/effective.yaml`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			env, err := configmgr.Load(opts.configPath)
			if err != nil {
				return err
			}

			if opts.json {
				generic, err := env.Pruned()
				if err != nil {
					return err
				}
				return printJSON(cmd, generic)
			}

			encoded, err := env.PrunedYAML()
			if err != nil {
				return err
			}
			writer := out(cmd)
			fmt.Fprintf(writer, "# %s\n", env.ConfigPath)
			fmt.Fprint(writer, string(encoded))
			return nil
		},
	}
}

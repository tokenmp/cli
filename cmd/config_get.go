package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tokenmp/cli/internal/configmgr"
)

func newConfigGetCmd(opts *configOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "get <路径>",
		Short: "取一个配置字段",
		Long: `取一个配置字段。

路径用点号分隔，与配置文件同形，数字段表示数组下标。
例如 listen、log.level、channels.0.base_url、plugins.list.0.key。
取到的段落或列表按 JSON 输出。

看到的是填过默认值之后的取值，没写在文件里的字段也能取到。`,
		Example: `  tokenmp config get listen
  tokenmp config get log.level
  tokenmp config get channels.0.base_url
  tokenmp config get plugins --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			env, err := configmgr.Load(opts.configPath)
			if err != nil {
				return err
			}

			value, err := env.Value(args[0])
			if err != nil {
				return err
			}
			if opts.json {
				return printJSON(cmd, value)
			}
			return printValue(cmd, value)
		},
	}
}

// printValue 打印一个配置取值：标量按原样，段落与列表按 JSON。
func printValue(cmd *cobra.Command, value any) error {
	switch value.(type) {
	case map[string]any, []any:
		return printJSON(cmd, value)
	case nil:
		fmt.Fprintln(out(cmd), "null")
	default:
		fmt.Fprintf(out(cmd), "%v\n", value)
	}
	return nil
}

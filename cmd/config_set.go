package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tokenmp/cli/internal/configmgr"
)

func newConfigSetCmd(opts *configOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "set <路径> <值>",
		Short: "改一个配置字段",
		Long: `改一个配置字段。

只改标量字段：路径必须已经存在，或者属于下面这几个顶层标量。
顶层标量是 listen、log.format、log.level、plugins.dir。
指向段落或列表的路径会被拒绝，整体替换段落几乎总是笔误。

改写保留注释与缩进，空行可能被规范化。
写回后重新加载校验，配置不合法就还原原文件。
因此把 log.level 改成不认识的取值会失败，而不是留下一份坏配置。`,
		Example: `  tokenmp config set listen 127.0.0.1:8787
  tokenmp config set log.level debug
  tokenmp config set log.format json
  tokenmp config set plugins.dir /srv/tokenmp/plugins`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			env, err := configmgr.Load(opts.configPath)
			if err != nil {
				return err
			}
			if err := env.Set(args[0], args[1]); err != nil {
				return err
			}

			writer := out(cmd)
			fmt.Fprintf(writer, "%s = %s %s\n",
				palette.program.Render(args[0]),
				palette.title.Render(args[1]),
				palette.label.Render("("+env.ConfigPath+")"),
			)
			return nil
		},
	}
}

package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tokenmp/cli/internal/version"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "打印本次构建的版本",
		Long: `打印本次构建的版本。

列出的提交号、构建时间与 Go 版本都是打包时记录下来的值。
没有注入版本元数据的构建会退回 Go 工具链嵌进二进制的信息，缺失的字段不打印。`,
		Example: `  tokenmp version
  tokenmp --version`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			printVersion(cmd)
			return nil
		},
	}
}

// printVersion 打印构建元数据：一行标题加一组对齐的字段。
func printVersion(cmd *cobra.Command) {
	info := version.Current()

	writer := out(cmd)
	fmt.Fprintf(writer, "%s %s\n\n", palette.program.Render("tokenmp"), palette.title.Render(info.Version))

	var rows [][2]string
	for _, row := range [][2]string{
		{"commit", info.Commit},
		{"built", info.Date},
		{"go", info.GoVersion},
		{"platform", info.Platform},
	} {
		if row[1] == "" {
			continue
		}
		rows = append(rows, row)
	}
	printFields(writer, rows)
}

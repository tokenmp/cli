package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tokenmp/cli/internal/configmgr"
)

// configPathOutput 是 path 命令 --json 的输出形状。
type configPathOutput struct {
	Path       string `json:"path"`
	Exists     bool   `json:"exists"`
	Dir        string `json:"dir"`
	Listen     string `json:"listen"`
	PluginsDir string `json:"pluginsDir"`
}

func newConfigPathCmd(opts *configOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "path",
		Short: "打印配置文件的路径与状态",
		Long: `打印配置文件的路径与状态。

配置文件不存在时也照常输出，脚本可以直接从这里取路径。路径是绝对路径，插件目录等
相对路径都以它所在目录为基准；配置文件不存在时列出的是默认值。`,
		Example: `  tokenmp config path
  tokenmp config path --config /etc/tokenmp/config.yaml
  tokenmp config path --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			env, err := configmgr.Open(opts.configPath)
			if err != nil {
				return err
			}

			result := configPathOutput{
				Path:       env.ConfigPath,
				Exists:     env.HasConfig,
				Dir:        env.Dir,
				Listen:     env.Config.Listen,
				PluginsDir: env.Config.Plugins.Dir,
			}
			if opts.json {
				return printJSON(cmd, result)
			}

			writer := out(cmd)
			printFields(writer, [][2]string{
				{"path", result.Path},
				{"state", stateText(result.Exists)},
				{"dir", result.Dir},
				{"listen", result.Listen},
				{"plugins.dir", result.PluginsDir},
			})
			if !result.Exists {
				fmt.Fprintf(writer, "\n%s\n", palette.warn.Render("配置文件不存在，上面几项是默认值；tokenmp config init 可以生成一份"))
			}
			return nil
		},
	}
}

// stateText 渲染文件是否存在。
func stateText(exists bool) string {
	if exists {
		return palette.ok.Render("已存在")
	}
	return palette.warn.Render("不存在")
}

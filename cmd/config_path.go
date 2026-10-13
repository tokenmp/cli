package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tokenmp/cli/internal/configmgr"
)

// configPathOutput 是 path 命令的输出形状。
//
// 路径与状态三项是这命令自己的概念；顶层标量的取值另放一段，键是配置路径
// （listen、log.format、defaults.max_body_bytes 这些），取值与配置文件里写的形状一致，
// 由 configmgr 按 core 的键表现算，因此 core 的配置加减一个顶层标量时这段跟着变。
type configPathOutput struct {
	Path    string         `json:"path"`
	Exists  bool           `json:"exists"`
	Dir     string         `json:"dir"`
	Scalars map[string]any `json:"scalars"`
}

func newConfigPathCmd(opts *configOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "path",
		Short: "打印配置文件的路径与状态",
		Long: fmt.Sprintf(`打印配置文件的路径与状态。

配置文件不存在时也照常输出，脚本可以直接从这里取路径。
路径是绝对路径，插件目录等相对路径都以它所在目录为基准。

顶层标量一栏由 core 的键表现算，路径是 %s 这些：整份配置只取一个值的键。
取值给的是生效取值，配置文件里写了哪一项由（默认值）标出 —— 标了的那几项没写在
文件里，由 core 在用到时按默认值兜底。--json 给的是同口径的形状，scalars 里为
null 的项同样是没写的项。`,
			joinList(configmgr.ScalarPaths())),
		Example: `  tokenmp config path
  tokenmp config path --config /etc/tokenmp/config.yaml
  tokenmp config path --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			env, err := configmgr.Open(opts.configPath)
			if err != nil {
				return err
			}

			scalars, err := env.Scalars()
			if err != nil {
				return err
			}
			result := configPathOutput{
				Path:    env.ConfigPath,
				Exists:  env.HasConfig,
				Dir:     env.Dir,
				Scalars: make(map[string]any, len(scalars)),
			}
			rows := [][2]string{
				{"path", result.Path},
				{"state", stateText(result.Exists)},
				{"dir", result.Dir},
			}
			for _, scalar := range scalars {
				result.Scalars[scalar.Path] = scalar.Resolved
				rows = append(rows, [2]string{scalar.Path, scalarText(scalar)})
			}

			if opts.json {
				return printJSON(cmd, result)
			}

			writer := out(cmd)
			printFields(writer, rows)
			if !result.Exists {
				fmt.Fprintf(writer, "\n%s\n", palette.warn.Render("配置文件不存在，上面几项是默认值；tokenmp config init 可以生成一份"))
			}
			return nil
		},
	}
}

// scalarText 呈现一条顶层标量的生效取值，并标出文件里没写、由 core 兜底的那几项。
func scalarText(scalar configmgr.Scalar) string {
	if scalar.Defaulted() {
		return fmt.Sprintf("%v %s", scalar.Text(), palette.label.Render("（默认值）"))
	}
	return fmt.Sprintf("%v", scalar.Text())
}

// stateText 渲染文件是否存在。
func stateText(exists bool) string {
	if exists {
		return palette.ok.Render("已存在")
	}
	return palette.warn.Render("不存在")
}

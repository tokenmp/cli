package cmd

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/tokenmp/cli/internal/configmgr"
)

// errConfigInvalid 让校验命令以非零状态退出。问题明细已经打印过。
var errConfigInvalid = errors.New("配置校验未通过")

// configCheck 是校验结果，也是 --json 的输出形状。
type configCheck struct {
	Path           string `json:"path"`
	Exists         bool   `json:"exists"`
	Valid          bool   `json:"valid"`
	Error          string `json:"error,omitempty"`
	Channels       int    `json:"channels"`
	Models         int    `json:"models"`
	EnabledPlugins int    `json:"enabledPlugins"`
}

func newConfigValidateCmd(opts *configOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "validate",
		Short: "校验配置并列出全部问题",
		Long: `校验配置并列出全部问题。

校验一次报出所有问题，下面这些都会列出来：

- 字段拼错
- 渠道重名
- 方言不支持
- 模型规则引用了不存在的渠道
- 插件 key 重复

配置文件不存在算不通过。

配置不合法时以状态码 1 退出，配置文件路径不存在时也不通过。`,
		Example: `  tokenmp config validate
  tokenmp config validate --config /etc/tokenmp/config.yaml
  tokenmp config validate --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			absolute, err := filepath.Abs(opts.configPath)
			if err != nil {
				return fmt.Errorf("解析配置文件路径失败: %w", err)
			}

			cfg, hasConfig, inspectErr := configmgr.Inspect(opts.configPath)
			result := configCheck{Path: absolute, Exists: hasConfig, Valid: inspectErr == nil}
			switch {
			case !hasConfig:
				// 没东西可校验也算不通过：多半是路径写错了。
				result.Valid = false
				result.Error = fmt.Sprintf("配置文件 %s 不存在", absolute)
			case inspectErr != nil:
				result.Error = inspectErr.Error()
			default:
				result.Channels = len(cfg.Channels)
				result.Models = len(cfg.Models)
				result.EnabledPlugins = len(cfg.EnabledPlugins())
			}

			if opts.json {
				if err := printJSON(cmd, result); err != nil {
					return err
				}
			} else {
				renderCheck(cmd, result)
			}

			if !result.Valid {
				return errConfigInvalid
			}
			return nil
		},
	}
}

// renderCheck 打印校验结果。
func renderCheck(cmd *cobra.Command, result configCheck) {
	writer := out(cmd)
	if !result.Valid {
		fmt.Fprintf(writer, "%s\n\n%s\n", palette.fail.Render(result.Path+" 不合法"), result.Error)
		return
	}

	fmt.Fprintf(writer, "%s\n\n", palette.ok.Render(result.Path+" 合法"))
	printFields(writer, [][2]string{
		{"渠道", fmt.Sprintf("%d 个", result.Channels)},
		{"模型规则", fmt.Sprintf("%d 条", result.Models)},
		{"启用插件", fmt.Sprintf("%d 个", result.EnabledPlugins)},
	})
}

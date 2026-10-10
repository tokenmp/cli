package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/tokenmp/core/plugin"

	"github.com/tokenmp/cli/internal/pluginmgr"
)

// pluginDetail 是 show 命令 --json 的输出形状。
type pluginDetail struct {
	pluginmgr.Summary
	// Config 是配置里该插件的 config 段。
	Config map[string]any `json:"config"`
	// Limits 是宿主对该插件生效的限额。
	Limits plugin.Limits `json:"limits"`
	// SourceBytes 是插件源码的字节数。
	SourceBytes int64 `json:"sourceBytes"`
}

func newPluginShowCmd(opts *pluginOptions) *cobra.Command {
	return &cobra.Command{
		Use:     "show <key>",
		Aliases: []string{"info"},
		Short:   "显示单个插件的自述信息与状态",
		Long: `显示单个插件的自述信息与状态。

自述信息来自插件导出的 meta，状态来自配置。
宿主对插件生效的限额与源码大小一并列出，便于判断插件是否超标。`,
		Example: `  # 自述信息、状态、限额与 config 段
  tokenmp plugin show echo

  # 只看机器可读的部分
  tokenmp plugin show echo --json

  # 插件不在默认目录里
  tokenmp plugin show echo --dir /srv/tokenmp/plugins`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			env, err := pluginmgr.Load(opts.configPath, opts.dir)
			if err != nil {
				return err
			}
			key := args[0]
			loaded, err := env.Find(key)
			if err != nil {
				return err
			}

			detail := pluginDetail{
				Summary:     stateOf(env, loaded),
				Config:      env.Config.PluginConfig(key),
				Limits:      loaded.Limits(),
				SourceBytes: sourceSize(loaded.Path()),
			}

			if opts.json {
				return printJSON(cmd, detail)
			}

			writer := out(cmd)
			manifest := loaded.Manifest()
			fmt.Fprintf(writer, "%s %s\n\n", palette.program.Render("plugin"), palette.title.Render(key))

			printFields(writer, [][2]string{
				{"name", dash(manifest.Name)},
				{"version", dash(manifest.Version)},
				{"apiVersion", dash(manifest.APIVersion)},
				{"description", dash(manifest.Description)},
				{"hooks", joinHooks(manifest.Hooks)},
				{"path", loaded.Path()},
				{"size", fmt.Sprintf("%d 字节", detail.SourceBytes)},
				{"state", detail.summaryText()},
				{"config", compactJSON(detail.Config)},
			})
			return nil
		},
	}
}

// compactJSON 把值编码成一行 JSON，供字段表使用。
func compactJSON(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "-"
	}
	return string(encoded)
}

// stateOf 取一个已编译插件在配置里的状态。
func stateOf(env *pluginmgr.Env, loaded *plugin.Loaded) pluginmgr.Summary {
	for _, entry := range env.Config.Plugins.List {
		if entry.Key == loaded.Key() {
			return pluginmgr.State{Loaded: loaded, Entry: entry, Registered: true}.Summary()
		}
	}
	return pluginmgr.State{Loaded: loaded}.Summary()
}

// sourceSize 返回插件源码的字节数，读不到时返回 0。
func sourceSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

// summaryText 把状态写成人读的一行，顺序只在启用时出现。
func (d pluginDetail) summaryText() string {
	switch {
	case !d.Registered:
		return palette.warn.Render("未登记，转发时不执行")
	case d.Enabled:
		return fmt.Sprintf("%s，顺序 %d", palette.ok.Render("启用"), d.Order)
	default:
		return palette.label.Render("停用")
	}
}

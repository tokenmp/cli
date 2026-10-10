package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tokenmp/core/config"

	"github.com/tokenmp/cli/internal/configmgr"
)

// show 命令支持的输出格式。
const (
	formatUI   = "ui"
	formatYAML = "yaml"
	formatJSON = "json"
)

// showFormats 是 --format 的取值，顺序即文档里的顺序。
var showFormats = []string{formatUI, formatYAML, formatJSON}

// pluginEntryRow 是配置里一条插件登记项，也是概览视图的一行。
type pluginEntryRow struct {
	Key     string         `json:"key"`
	Enabled bool           `json:"enabled"`
	Order   int            `json:"order"`
	Config  map[string]any `json:"config,omitempty"`
}

func newConfigShowCmd(opts *configOptions) *cobra.Command {
	var format string

	cmd := &cobra.Command{
		Use:   "show",
		Short: "打印生效配置",
		Long: `打印生效配置。

默认输出概览视图：服务、渠道、模型路由与插件清单分组排列，渠道表与 config channels
同形。--format yaml 给出填过默认值的配置文本，可以重定向成一份新配置；--format json
给出机器可读的形状。--json 等同于 --format json。

三种格式看到的都是生效配置，不是文件原文，因此没写在文件里的 listen、timeout、
max_retries 也会出现。yaml 与 json 不打印空值字段：空字符串、空段落与空列表只表示
没写，没有信息量。`,
		Example: `  tokenmp config show
  tokenmp config show --format yaml > /tmp/effective.yaml
  tokenmp config show --format json
  tokenmp config show --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			env, err := configmgr.Load(opts.configPath)
			if err != nil {
				return err
			}

			// --json 是整组通用的开关，它压过 --format。
			selected := format
			if opts.json {
				selected = formatJSON
			}

			switch selected {
			case formatUI:
				renderConfigOverview(cmd, env)
				return nil
			case formatYAML:
				return printConfigYAML(cmd, env)
			case formatJSON:
				generic, err := env.Pruned()
				if err != nil {
					return err
				}
				return printJSON(cmd, generic)
			default:
				return fmt.Errorf("--format 只支持 %s，实际是 %q", strings.Join(showFormats, "、"), selected)
			}
		},
	}

	// 说明里要留一个空格：fang 只对第一个空格分隔的词做首字母大写，整串没有空格时
	// 它会把 ui、yaml、json 一起改写成 Ui、Yaml、Json。
	cmd.Flags().StringVar(&format, "format", formatUI, "输出格式，可选 "+strings.Join(showFormats, "、"))
	return cmd
}

// printConfigYAML 打印生效配置的 YAML 文本。
func printConfigYAML(cmd *cobra.Command, env *configmgr.Env) error {
	encoded, err := env.PrunedYAML()
	if err != nil {
		return err
	}
	writer := out(cmd)
	fmt.Fprintf(writer, "# %s\n", env.ConfigPath)
	fmt.Fprint(writer, string(encoded))
	return nil
}

// renderConfigOverview 打印概览视图：分组标题加表格，配色与其他命令一致。
func renderConfigOverview(cmd *cobra.Command, env *configmgr.Env) {
	cfg := env.Config
	writer := out(cmd)

	fmt.Fprintf(writer, "%s %s\n\n", palette.program.Render("config"), palette.cell.Render(env.ConfigPath))

	fmt.Fprintln(writer, palette.title.Render("服务"))
	service := newTable().Headers("项", "值")
	service.Row("listen", cfg.Listen)
	service.Row("log.format", cfg.Log.Format)
	service.Row("log.level", cfg.Log.Level)
	fmt.Fprintln(writer, service.Render())

	channels := channelRows(cfg)
	fmt.Fprintf(writer, "\n%s\n", sectionTitle("渠道", fmt.Sprintf("%d 个", len(channels))))
	if len(channels) > 0 {
		renderer := newTable().Headers("渠道", "方言", "基址", "路径", "鉴权", "凭据", "超时", "重试")
		var missing []string
		for _, row := range channels {
			if row.Credential == credentialMissing {
				missing = append(missing, row.Name)
			}
			renderer.Row(row.Name, row.Dialect, row.BaseURL, dash(row.Path), row.Auth, credentialText(row.Credential), row.Timeout, fmt.Sprintf("%d", row.MaxRetries))
		}
		fmt.Fprintln(writer, renderer.Render())
		if len(missing) > 0 {
			fmt.Fprintf(writer, "%s%s\n", palette.warn.Render("凭据环境变量没有设置："), joinList(missing))
		}
	}

	rules := ruleRows(cfg)
	fmt.Fprintf(writer, "\n%s\n", sectionTitle("模型路由", fmt.Sprintf("%d 条", len(rules))))
	if len(rules) > 0 {
		renderer := newTable().Headers("匹配", "渠道", "上游模型", "方言")
		for _, rule := range rules {
			renderer.Row(rule.Match, rule.Channel, upstreamText(rule.Upstream), rule.Dialect)
		}
		fmt.Fprintln(writer, renderer.Render())
	}

	plugins := pluginEntryRows(cfg)
	fmt.Fprintf(writer, "\n%s %s\n", palette.label.Render("插件目录"), palette.cell.Render(cfg.Plugins.Dir))
	enabled := len(cfg.EnabledPlugins())
	fmt.Fprintf(writer, "%s\n", sectionTitle("插件清单", fmt.Sprintf("%d 条，%d 个启用", len(plugins), enabled)))
	if len(plugins) > 0 {
		renderer := newTable().Headers("插件", "状态", "顺序", "配置")
		for _, row := range plugins {
			renderer.Row(row.Key, entryStateText(row.Enabled), fmt.Sprintf("%d", row.Order), entryConfigText(row.Config))
		}
		fmt.Fprintln(writer, renderer.Render())
	}
}

// sectionTitle 渲染一个小节标题：标题加灰色的计数。
func sectionTitle(title, count string) string {
	return palette.title.Render(title) + "  " + palette.label.Render(count)
}

// entryStateText 渲染插件登记项的启用状态。
func entryStateText(enabled bool) string {
	if enabled {
		return palette.ok.Render("启用")
	}
	return palette.label.Render("停用")
}

// entryConfigText 渲染插件登记项的 config 段，空配置显示占位符。
func entryConfigText(entry map[string]any) string {
	if len(entry) == 0 {
		return "-"
	}
	return compactJSON(entry)
}

// pluginEntryRows 把配置里的插件登记项整理成待输出的形状。
func pluginEntryRows(cfg *config.Config) []pluginEntryRow {
	rows := make([]pluginEntryRow, 0, len(cfg.Plugins.List))
	for _, entry := range cfg.Plugins.List {
		rows = append(rows, pluginEntryRow{
			Key:     entry.Key,
			Enabled: entry.Enabled,
			Order:   entry.Order,
			Config:  entry.Config,
		})
	}
	return rows
}

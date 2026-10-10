package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tokenmp/core/plugin"
)

// hookTiming 是四个钩子的时机与负载说明，与 core 的插件规范一致。
var hookTiming = map[plugin.Hook]struct {
	When   string
	Input  string
	Output string
}{
	plugin.HookRequest:   {When: "选路之前", Input: "dialect、model、stream、body", Output: "model、body"},
	plugin.HookEvent:     {When: "每条流式事件写回之前", Input: "dialect、event、data", Output: "event、data"},
	plugin.HookStreamEnd: {When: "流结束、终止帧写出之前", Input: "dialect、model、usage、events", Output: "events"},
	plugin.HookResponse:  {When: "非流式响应写回之前", Input: "dialect、model、body", Output: "body"},
}

func newPluginHooksCmd(opts *pluginOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "hooks",
		Short: "列出宿主提供的钩子与限额",
		Long: `列出宿主提供的钩子与限额。

钩子按请求生命周期的顺序排列。
钩子必须是同步函数，返回 Promise 会被拒绝；
返回 null 或 undefined 表示不改写，宿主沿用前一个插件的输出。
限额由宿主统一设置，插件不能自行放宽。`,
		Args: cobra.NoArgs,
		Example: `  tokenmp plugin hooks
  tokenmp plugin hooks --json`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			limits := plugin.DefaultLimits()

			if opts.json {
				payload := struct {
					APIVersion string              `json:"apiVersion"`
					Hooks      []plugin.Hook       `json:"hooks"`
					Timing     map[string]hookNote `json:"timing"`
					Limits     plugin.Limits       `json:"limits"`
				}{
					APIVersion: plugin.APIVersion,
					Hooks:      plugin.Hooks(),
					Timing:     hookNotes(),
					Limits:     limits,
				}
				return printJSON(cmd, payload)
			}

			writer := out(cmd)
			fmt.Fprintf(writer, "%s %s\n\n", palette.program.Render("plugin hooks"), palette.label.Render("契约版本 "+plugin.APIVersion))

			hooks := newTable().Headers("钩子", "时机", "输入", "输出")
			for _, hook := range plugin.Hooks() {
				note := hookTiming[hook]
				hooks.Row(string(hook), note.When, note.Input, note.Output)
			}
			fmt.Fprintln(writer, hooks.Render())

			fmt.Fprintf(writer, "\n%s\n", palette.title.Render("限额"))
			limitsTable := newTable().Headers("限额", "取值", "含义")
			limitsTable.Row("源码", fmt.Sprintf("%d 字节", limits.SourceBytes), "单个插件文件的字节上限")
			limitsTable.Row("负载", fmt.Sprintf("%d 字节", limits.PayloadBytes), "单次调用的输入与输出上限")
			limitsTable.Row("超时", limits.Timeout.String(), "单次调用的上限，超时中断插件")
			limitsTable.Row("并发", fmt.Sprintf("%d", limits.Concurrency), "同一插件同时执行的钩子数")
			fmt.Fprintln(writer, limitsTable.Render())
			return nil
		},
	}
}

// hookNote 是钩子说明的 JSON 形状。
type hookNote struct {
	When   string `json:"when"`
	Input  string `json:"input"`
	Output string `json:"output"`
}

// hookNotes 把钩子说明转成可按名字取的映射，供 JSON 输出使用。
func hookNotes() map[string]hookNote {
	notes := make(map[string]hookNote, len(hookTiming))
	for hook, note := range hookTiming {
		notes[string(hook)] = hookNote{When: note.When, Input: note.Input, Output: note.Output}
	}
	return notes
}

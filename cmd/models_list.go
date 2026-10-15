package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tokenmp/cli/internal/configmgr"
)

func newModelsListCmd(opts *modelsOptions) *cobra.Command {
	var (
		model   string
		jsonOut bool
	)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "列出模型路由规则，或试算一个模型的选路",
		Long: `列出模型路由规则，或试算一个模型的选路。

不带 --model 时只列出规则：
 - 序号是规则在 models 段里的下标，与 config get models.0 的口径一致
 - match 支持 * 与 ? 通配，同一个模型可以有多条规则，按配置顺序依次尝试
 - upstream 为空表示用请求里的模型名；写了 ${model} 就替换成请求里的模型名
 - dialect 为空表示用渠道的方言
 - affinity 是渠道亲和的组名，同组候选在运行时可互换，留空表示不参与

带 --model 时按规则试算，列出这个模型名会按什么顺序落到哪些渠道上，
upstream 里的占位符按这个模型名替换后给出。没有匹配的规则时列表为空。

试算给出的是配置顺序。配了 affinity 时，同一个组的候选在运行时还会按会话标识
重排，粘住其中一条；重排的结果取决于请求，试算给不出来，所以遇到这种情况会
在表后说明一句。`,
		Example: `  # 看规则表
  tokenmp models list

  # 试算一个模型名的候选路由
  tokenmp models list --model gpt-4o
  tokenmp models list --model claude-sonnet-4 --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// 规则表与序号按文件里的写法：序号与 config get models.N 的口径一致。
			env, err := configmgr.Load(opts.configPath)
			if err != nil {
				return err
			}

			output := modelsOutput{Rules: ruleRows(env.Config)}
			if model != "" {
				output.Model = model
				// 试算按运行时形态：展开顶层账号之后的渠道与规则才是真正的候选。
				// Expand 是导出的且幂等，这里直接用。
				output.Routes = routeRows(env.Config.Expand(), model)
			}

			if jsonOut {
				return printJSON(cmd, output)
			}

			hasRules, err := printModelsRules(cmd, output)
			if err != nil {
				return err
			}
			if !hasRules || model == "" {
				return nil
			}

			writer := out(cmd)
			fmt.Fprintf(writer, "\n%s\n", palette.title.Render("模型 "+model+" 的候选路由"))
			if len(output.Routes) == 0 {
				fmt.Fprintf(writer, "%s\n", palette.warn.Render("没有匹配的规则"))
				return nil
			}
			routes := newTable().Headers("顺序", "渠道", "上游模型", "方言", "亲和")
			for index, route := range output.Routes {
				routes.Row(fmt.Sprintf("%d", index+1), route.Channel, route.Upstream, route.Dialect, dash(route.Affinity))
			}
			fmt.Fprintln(writer, routes.Render())
			if group, count := affinityReorderable(output.Routes); group != "" {
				fmt.Fprintf(writer, "%s\n", palette.warn.Render(fmt.Sprintf(
					"注：亲和组 %s 的 %d 条候选在运行时按会话标识重排，上表给的是配置顺序。",
					group, count)))
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&model, "model", "", "按这个模型名试算候选路由")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "以 JSON 输出，便于脚本处理")
	return cmd
}

// modelTable 把路由规则整理成表格形态，models list 与交互浏览共用这一份单元格。
func modelTable(output modelsOutput) browseTable {
	table := browseTable{
		headers: []string{"序号", "匹配", "渠道", "上游模型", "方言", "亲和"},
		empty:   "配置里没有模型路由规则",
	}
	for _, rule := range output.Rules {
		table.rows = append(table.rows, []string{
			fmt.Sprintf("%d", rule.Index), rule.Match, rule.Channel,
			upstreamText(rule.Upstream), rule.Dialect, dash(rule.Affinity),
		})
	}
	return table
}

// printModelsRules 打印路由规则表，models list 与交互模式的非终端退化共用。
//
// 第二个返回值报告是否有规则：没有规则时表头也不打印，调用方据此跳过后续的试算表。
func printModelsRules(cmd *cobra.Command, output modelsOutput) (bool, error) {
	writer := out(cmd)
	table := modelTable(output)
	if len(table.rows) == 0 {
		fmt.Fprintln(writer, table.empty)
		return false, nil
	}

	fmt.Fprintln(writer, palette.title.Render("路由规则"))
	fmt.Fprintln(writer, table.render(table.all()))
	return true, nil
}

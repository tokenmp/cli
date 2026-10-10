package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tokenmp/core/config"

	"github.com/tokenmp/cli/internal/configmgr"
)

// modelRow 是一条模型路由规则，也是 --json 的输出形状。
type modelRow struct {
	Match    string `json:"match"`
	Channel  string `json:"channel"`
	Upstream string `json:"upstream"`
	Dialect  string `json:"dialect"`
	Affinity string `json:"affinity,omitempty"`
}

// routeRow 是解析后的一条候选路由。
type routeRow struct {
	Channel  string `json:"channel"`
	Upstream string `json:"upstream"`
	Dialect  string `json:"dialect"`
	Affinity string `json:"affinity,omitempty"`
}

// modelsOutput 是 models 命令 --json 的输出形状。
type modelsOutput struct {
	Rules  []modelRow `json:"rules"`
	Model  string     `json:"model,omitempty"`
	Routes []routeRow `json:"routes"`
}

func newConfigModelsCmd(opts *configOptions) *cobra.Command {
	var model string

	cmd := &cobra.Command{
		Use:   "models",
		Short: "列出模型路由规则，或试算一个模型的选路",
		Long: `列出模型路由规则，或试算一个模型的选路。

不带 --model 时只列出规则：
 - match 支持 * 与 ? 通配，同一个模型可以有多条规则，按配置顺序依次尝试
 - upstream 为空表示用请求里的模型名；写了 ${model} 就替换成请求里的模型名
 - dialect 为空表示用渠道的方言
 - affinity 是渠道亲和的组名，同组候选在运行时可互换，留空表示不参与

带 --model 时按规则试算，列出这个模型名会按什么顺序落到哪些渠道上，
upstream 里的占位符按这个模型名替换后给出。没有匹配的规则时列表为空。`,
		Example: `  tokenmp config models
  tokenmp config models --model gpt-4o
  tokenmp config models --model claude-sonnet-4 --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			env, err := configmgr.Load(opts.configPath)
			if err != nil {
				return err
			}

			output := modelsOutput{Rules: ruleRows(env.Config)}
			if model != "" {
				output.Model = model
				output.Routes = routeRows(env.Config, model)
			}

			if opts.json {
				return printJSON(cmd, output)
			}

			writer := out(cmd)
			if len(output.Rules) == 0 {
				fmt.Fprintln(writer, "配置里没有模型路由规则")
				return nil
			}

			fmt.Fprintln(writer, palette.title.Render("路由规则"))
			rules := newTable().Headers("匹配", "渠道", "上游模型", "方言", "亲和")
			for _, rule := range output.Rules {
				rules.Row(rule.Match, rule.Channel, upstreamText(rule.Upstream), rule.Dialect, dash(rule.Affinity))
			}
			fmt.Fprintln(writer, rules.Render())

			if model == "" {
				return nil
			}

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
			return nil
		},
	}

	cmd.Flags().StringVar(&model, "model", "", "按这个模型名试算候选路由")
	return cmd
}

// ruleRows 把路由规则整理成待输出的形状。
func ruleRows(cfg *config.Config) []modelRow {
	rows := make([]modelRow, 0, len(cfg.Models))
	for _, rule := range cfg.Models {
		rows = append(rows, modelRow{
			Match:    rule.Match,
			Channel:  rule.Channel,
			Upstream: rule.Upstream,
			Dialect:  ruleDialect(cfg, rule),
			Affinity: rule.Affinity,
		})
	}
	return rows
}

// ruleDialect 给出规则实际生效的方言：规则没写方言时取渠道的方言。
func ruleDialect(cfg *config.Config, rule config.Model) string {
	if rule.Dialect != "" {
		return rule.Dialect
	}
	if channel, ok := cfg.ChannelByName(rule.Channel); ok {
		return channel.Dialect + "（同渠道）"
	}
	return "-"
}

// routeRows 试算一个模型名的候选路由。
func routeRows(cfg *config.Config, model string) []routeRow {
	routes := cfg.Resolve(model)
	rows := make([]routeRow, 0, len(routes))
	for _, route := range routes {
		rows = append(rows, routeRow{
			Channel:  route.Channel,
			Upstream: route.Upstream,
			Dialect:  string(route.Dialect),
			Affinity: route.Affinity,
		})
	}
	return rows
}

// upstreamText 说明上游模型名的来源。
func upstreamText(upstream string) string {
	if upstream == "" {
		return "同请求的模型名"
	}
	return upstream
}

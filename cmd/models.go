package cmd

import (
	"github.com/spf13/cobra"

	"github.com/tokenmp/core/config"
)

// modelsOptions 是 models 命令组共用的选项。
type modelsOptions struct {
	// configPath 是配置文件路径。
	configPath string
}

// newModelsCmd 组装模型路由命令组。
func newModelsCmd() *cobra.Command {
	opts := &modelsOptions{}

	cmd := &cobra.Command{
		Use:   "models",
		Short: "管理模型路由规则",
		Long: `管理模型路由规则。

一条规则把一类模型名绑到一个渠道，写在配置的 models 段，按配置顺序依次尝试。
本组命令只读写配置，改动在服务重启后生效。

- match 支持 * 与 ? 通配，同一个模型可以有多条规则
- upstream 为空表示用请求里的模型名；写了 ${model} 就替换成请求里的模型名
- dialect 为空表示用渠道的方言
- affinity 是渠道亲和的组名，同组候选在运行时可互换

规则没有名字，list 的表里给出序号，口径与 config get models.0 的下标一致；
delete 收这个序号，也接受 --match 加 --channel 的定位写法。
create 引用了不存在的渠道时报错，重名的规则不覆盖。`,
		Example: `  # 看现有规则
  tokenmp models list

  # 试算一个模型名的候选路由
  tokenmp models list --model deepseek-chat

  # 加一条规则，把 deepseek-* 交给 commandcode
  tokenmp models create --match "deepseek-*" --channel commandcode --upstream "deepseek/${model}"

  # 按序号或按匹配式删除
  tokenmp models delete 0
  tokenmp models delete "deepseek-*" --channel commandcode`,
	}

	cmd.PersistentFlags().StringVar(&opts.configPath, "config", "config.yaml", "配置文件路径")

	cmd.AddCommand(
		newModelsListCmd(opts),
		newModelsCreateCmd(opts),
		newModelsDeleteCmd(opts),
	)

	return cmd
}

// modelRow 是一条模型路由规则，也是 --json 的输出形状。
//
// Index 是规则在 models 段里的下标，与 config get models.0 的口径一致，
// 也是 delete 按序号删除时用的那个序号。
type modelRow struct {
	Index    int    `json:"index"`
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

// modelEntry 是写回配置的模型规则条目。
//
// 空的可选字段不写出去：upstream 为空表示用请求里的模型名，dialect 为空表示用渠道
// 的方言，affinity 为空表示不参与亲和。
type modelEntry struct {
	Match    string `yaml:"match"`
	Channel  string `yaml:"channel"`
	Upstream string `yaml:"upstream,omitempty"`
	Dialect  string `yaml:"dialect,omitempty"`
	Affinity string `yaml:"affinity,omitempty"`
}

// modelEntryOf 把生效配置里的规则还原成写回形状，可选字段为空时不带出来。
func modelEntryOf(rule config.Model) modelEntry {
	return modelEntry{
		Match:    rule.Match,
		Channel:  rule.Channel,
		Upstream: rule.Upstream,
		Dialect:  rule.Dialect,
		Affinity: rule.Affinity,
	}
}

// ruleRows 把路由规则整理成待输出的形状。
func ruleRows(cfg *config.Config) []modelRow {
	rows := make([]modelRow, 0, len(cfg.Models))
	for index, rule := range cfg.Models {
		rows = append(rows, modelRow{
			Index:    index,
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

package cmd

import (
	"errors"

	"github.com/spf13/cobra"

	"github.com/tokenmp/core/config"

	"github.com/tokenmp/cli/internal/configmgr"
)

// modelsOptions 是 models 命令组共用的选项。
type modelsOptions struct {
	// configPath 是配置文件路径。
	configPath string
	// jsonOut 报告是否给了 --json。非终端时按 JSON 输出，进交互时报错。
	jsonOut bool
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
- affinity 是渠道亲和的组名，同组候选在运行时可互换，池内顺序按会话标识决定，
  list --model 的试算只给配置顺序

规则没有名字，list 的表里给出序号，口径与 config get models.0 的下标一致；
delete 收这个序号，也接受 --match 加 --channel 的定位写法。
create 引用了不存在的渠道时报错，重名的规则不覆盖。

不带子命令时进入分步管理：先打印与 list 相同的规则表，再问做什么。

- 先选新建、编辑、删除或退出；编辑与删除还要从列表里选一条
- 选一项后一个字段一个问题地问下去，编辑时每个字段的默认值是当前值，回车即保留
- channel 只能从配置里已有的渠道里选，match 用 core 的匹配式校验
- 每次只写变化过的字段，写回前打印将要写入的条目并再确认一次
- Esc 或 Ctrl-C 中断当前这件，回到菜单；在菜单上中断则退出，两种情况下都不落盘

输出不是终端时打印与 models list 相同的规则表，不做交互。--json 只在非终端时生效，进交互时报错。`,
		Example: `  # 看现有规则
  tokenmp models list

  # 试算一个模型名的候选路由
  tokenmp models list --model deepseek-chat

  # 加一条规则，把 deepseek-* 交给 commandcode
  tokenmp models create --match "deepseek-*" --channel commandcode --upstream "deepseek/${model}"

  # 按序号或按匹配式删除
  tokenmp models delete 0
  tokenmp models delete "deepseek-*" --channel commandcode`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runModels(cmd, opts, isTerminal(cmd))
		},
	}

	cmd.PersistentFlags().StringVar(&opts.configPath, "config", "config.yaml", "配置文件路径")
	cmd.Flags().BoolVar(&opts.jsonOut, "json", false, "以 JSON 输出，非终端时等价于 models list --json")

	cmd.AddCommand(
		newModelsListCmd(opts),
		newModelsCreateCmd(opts),
		newModelsDeleteCmd(opts),
	)

	return cmd
}

// runModels 执行 models 组不带子命令时的行为。
//
// interactive 报告输出是不是终端。抽成参数后测试可以直接走两个分支，
// 不必让测试进程拥有真的终端。
func runModels(cmd *cobra.Command, opts *modelsOptions, interactive bool) error {
	// 不是终端时与 models list 完全一致，脚本与管道拿到的是同一份输出。
	if !interactive {
		env, err := configmgr.Load(opts.configPath)
		if err != nil {
			return err
		}
		output := modelsOutput{Rules: ruleRows(env.Config)}
		if opts.jsonOut {
			return printJSON(cmd, output)
		}
		_, err = printModelsRules(cmd, output)
		return err
	}

	if opts.jsonOut {
		return errors.New("--json 与交互模式不能同时使用：去掉 --json，或把输出重定向到文件")
	}
	return runModelsManage(cmd, opts.configPath)
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

// modelEntryOf 把配置里的规则还原成写回形状，可选字段为空时不带出来。
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

// affinityReorderable 报告试算出的候选在运行时会不会被亲和重排，会重排时返回组名与池内候选数。
//
// 与 core 的 server.affinityPool 同口径：组名取第一条带组名的候选，只有组名相同的
// 候选组成一个池，池里不足两条时亲和没有可分配的对象，顺序与配置一致。试算没有会话
// 标识，重排的结果只有运行时才知道，所以这里只负责说明，不重排。
func affinityReorderable(routes []routeRow) (string, int) {
	group := ""
	for _, route := range routes {
		if route.Affinity != "" {
			group = route.Affinity
			break
		}
	}
	if group == "" {
		return "", 0
	}
	count := 0
	for _, route := range routes {
		if route.Affinity == group {
			count++
		}
	}
	if count < 2 {
		return "", 0
	}
	return group, count
}

// upstreamText 说明上游模型名的来源。
func upstreamText(upstream string) string {
	if upstream == "" {
		return "同请求的模型名"
	}
	return upstream
}

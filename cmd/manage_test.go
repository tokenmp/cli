package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/tokenmp/core/channel"
	"github.com/tokenmp/core/config"
	"github.com/tokenmp/core/dialect"

	"github.com/tokenmp/cli/internal/configmgr"
)

// browseConfig 有两条渠道与两条规则：primary 被两条规则引用，backup 没有被引用。
const browseConfig = `# 沙箱配置
listen: 127.0.0.1:9999

log:
  format: text

channels:
  # 主渠道，被规则引用
  - name: primary
    dialect: openai
    base_url: https://api.example.com/v1
    auth:
      style: bearer
      key_env: PRIMARY_KEY

  # 备用渠道，没有规则引用
  - name: backup
    dialect: claude
    base_url: https://api.backup.com
    auth:
      style: none

models:
  # 主规则
  - match: "gpt-*"
    channel: primary
  - match: "claude-*"
    channel: primary
`

// editChannelConfig 带一条字段齐全的渠道，用来验证编辑只写变化的部分并保留注释。
const editChannelConfig = `# 沙箱配置
listen: 127.0.0.1:9999

channels:
  # 主渠道
  - name: example
    dialect: openai
    base_url: https://api.example.com/v1
    path: /chat/completions
    auth:
      # 凭据变量
      style: bearer
      key_env: EXAMPLE_KEY
    max_retries: 2

models:
  # 主规则
  - match: "gpt-*"
    channel: example
`

// TestChannelsGroupFallsBackToTable 验证非终端下 channels 组与 channels list 输出一致。
func TestChannelsGroupFallsBackToTable(t *testing.T) {
	path := configSandbox(t, browseConfig)

	group, err := run(t, "channels", "--config", path)
	if err != nil {
		t.Fatalf("channels: %v\n%s", err, group)
	}
	list, err := run(t, "channels", "list", "--config", path)
	if err != nil {
		t.Fatalf("channels list: %v\n%s", err, list)
	}
	if group != list {
		t.Errorf("非终端时 channels 应当与 channels list 一致\nchannels:\n%s\nlist:\n%s", group, list)
	}
	for _, want := range []string{"primary", "backup", "凭据环境变量没有设置"} {
		if !strings.Contains(group, want) {
			t.Errorf("表格里没有 %q:\n%s", want, group)
		}
	}
}

// TestModelsGroupFallsBackToTable 验证非终端下 models 组与 models list 输出一致。
func TestModelsGroupFallsBackToTable(t *testing.T) {
	path := configSandbox(t, browseConfig)

	group, err := run(t, "models", "--config", path)
	if err != nil {
		t.Fatalf("models: %v\n%s", err, group)
	}
	list, err := run(t, "models", "list", "--config", path)
	if err != nil {
		t.Fatalf("models list: %v\n%s", err, list)
	}
	if group != list {
		t.Errorf("非终端时 models 应当与 models list 一致\nmodels:\n%s\nlist:\n%s", group, list)
	}
	for _, want := range []string{"gpt-*", "claude-*", "primary"} {
		if !strings.Contains(group, want) {
			t.Errorf("表格里没有 %q:\n%s", want, group)
		}
	}
}

// TestChannelsGroupJSONNonTTY 验证管道里给 --json 打印 JSON，而不是报互斥错误。
func TestChannelsGroupJSONNonTTY(t *testing.T) {
	path := configSandbox(t, browseConfig)

	group, err := run(t, "channels", "--config", path, "--json")
	if err != nil {
		t.Fatalf("channels --json: %v\n%s", err, group)
	}
	list, err := run(t, "channels", "list", "--config", path, "--json")
	if err != nil {
		t.Fatalf("channels list --json: %v\n%s", err, list)
	}
	if group != list {
		t.Errorf("两处 --json 输出应当一致\nchannels:\n%s\nlist:\n%s", group, list)
	}

	var rows []channelRow
	if err := json.Unmarshal([]byte(group), &rows); err != nil {
		t.Fatalf("输出不是 JSON 数组: %v\n%s", err, group)
	}
	if len(rows) != 2 {
		t.Errorf("JSON 里有 %d 条渠道，想要 2 条", len(rows))
	}
}

// TestModelsGroupJSONNonTTY 验证 models 组在管道里的 --json 行为。
func TestModelsGroupJSONNonTTY(t *testing.T) {
	path := configSandbox(t, browseConfig)

	group, err := run(t, "models", "--config", path, "--json")
	if err != nil {
		t.Fatalf("models --json: %v\n%s", err, group)
	}
	list, err := run(t, "models", "list", "--config", path, "--json")
	if err != nil {
		t.Fatalf("models list --json: %v\n%s", err, list)
	}
	if group != list {
		t.Errorf("两处 --json 输出应当一致\nmodels:\n%s\nlist:\n%s", group, list)
	}

	var output modelsOutput
	if err := json.Unmarshal([]byte(group), &output); err != nil {
		t.Fatalf("输出不是 JSON: %v\n%s", err, group)
	}
	if len(output.Rules) != 2 {
		t.Errorf("JSON 里有 %d 条规则，想要 2 条", len(output.Rules))
	}
}

// TestGroupJSONConflictsWithInteractive 验证进交互时 --json 报错。
//
// interactive 是 runChannels 与 runModels 的参数，测试直接给 true，不必有真的终端；
// 报错发生在进入表单之前。
func TestGroupJSONConflictsWithInteractive(t *testing.T) {
	path := configSandbox(t, browseConfig)

	if err := runChannels(&cobra.Command{}, &channelsOptions{configPath: path, jsonOut: true}, true); err == nil {
		t.Error("channels 进交互时给 --json 应当报错")
	} else if !strings.Contains(err.Error(), "--json") {
		t.Errorf("错误信息里应当点出 --json: %v", err)
	}

	if err := runModels(&cobra.Command{}, &modelsOptions{configPath: path, jsonOut: true}, true); err == nil {
		t.Error("models 进交互时给 --json 应当报错")
	} else if !strings.Contains(err.Error(), "--json") {
		t.Errorf("错误信息里应当点出 --json: %v", err)
	}
}

// TestGroupHelpStillWorks 验证 --help 与 help 仍然给帮助，不被交互挡住。
func TestGroupHelpStillWorks(t *testing.T) {
	for _, args := range [][]string{{"channels", "--help"}, {"help", "channels"}, {"models", "--help"}, {"help", "models"}} {
		out, err := run(t, args...)
		if err != nil {
			t.Fatalf("tokenmp %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		if !strings.Contains(out, "分步管理") {
			t.Errorf("tokenmp %s 的帮助里应当说明分步管理:\n%s", strings.Join(args, " "), out)
		}
	}
}

// TestChannelFormFromRecordKeepsCurrentValues 验证编辑时每个字段的默认值是当前值。
func TestChannelFormFromRecordKeepsCurrentValues(t *testing.T) {
	retries := 3
	record := config.Channel{
		Name:       "example",
		Dialect:    "claude",
		BaseURL:    "https://api.example.com",
		Path:       "/chat/completions",
		Auth:       config.Auth{Style: channel.StyleCustom, KeyEnv: "KEY", Header: "X-Key"},
		Timeout:    config.Duration(30_000_000_000),
		MaxRetries: &retries,
	}

	form := channelFormFromRecord(record)
	want := channelForm{
		Name:       "example",
		Dialect:    "claude",
		BaseURL:    "https://api.example.com",
		Path:       "/chat/completions",
		AuthStyle:  channel.StyleCustom,
		KeyEnv:     "KEY",
		Header:     "X-Key",
		Timeout:    "30s",
		MaxRetries: "3",
	}
	if form != want {
		t.Errorf("编辑默认值不对\n--- got ---\n%+v\n--- want ---\n%+v", form, want)
	}
}

// TestChannelFormDefaultsNormalizeEmptyAuthStyle 验证没写 style 的渠道按 none 处理。
func TestChannelFormDefaultsNormalizeEmptyAuthStyle(t *testing.T) {
	form := channelFormFromRecord(config.Channel{Name: "example", Timeout: config.Duration(120_000_000_000)})
	if form.AuthStyle != channel.StyleNone {
		t.Errorf("空 style 应当按 none 处理，实际是 %q", form.AuthStyle)
	}
}

// TestChannelRecordFromForm 验证表单组装出的渠道记录与校验。
func TestChannelRecordFromForm(t *testing.T) {
	form := newChannelForm()
	form.Name = "local"
	form.Dialect = "openai"
	form.BaseURL = "http://127.0.0.1:8080"

	record, err := channelRecordFromForm(form)
	if err != nil {
		t.Fatalf("channelRecordFromForm: %v", err)
	}
	if record.Auth.Style != channel.StyleNone {
		t.Errorf("默认鉴权应当是 none，实际是 %q", record.Auth.Style)
	}
	if record.Auth.KeyEnv != "" {
		t.Errorf("不鉴权时不该有 key_env: %q", record.Auth.KeyEnv)
	}
	if record.MaxRetries != nil {
		t.Errorf("没填重试时应当是 nil，实际 %v", *record.MaxRetries)
	}
	if record.Timeout != config.Duration(channel.DefaultTimeout) {
		t.Errorf("超时默认值不对: %v", record.Timeout)
	}

	form.AuthStyle = channel.StyleBearer
	form.KeyEnv = "LOCAL_KEY"
	form.MaxRetries = "4"
	record, err = channelRecordFromForm(form)
	if err != nil {
		t.Fatalf("channelRecordFromForm: %v", err)
	}
	if record.Auth.KeyEnv != "LOCAL_KEY" || record.Auth.Style != channel.StyleBearer {
		t.Errorf("鉴权字段不对: %+v", record.Auth)
	}
	if record.MaxRetries == nil || *record.MaxRetries != 4 {
		t.Errorf("重试次数不对: %v", record.MaxRetries)
	}

	form.BaseURL = "not-a-url"
	if _, err := channelRecordFromForm(form); err == nil {
		t.Error("非法基址应当报错")
	}
}

// TestValidateChannelFields 验证逐步校验：必填、基址、超时与重试。
func TestValidateChannelFields(t *testing.T) {
	if err := validateChannelName("  "); err == nil {
		t.Error("空渠道名应当报错")
	}
	if err := validateChannelName("local"); err != nil {
		t.Errorf("合法渠道名不该报错: %v", err)
	}

	form := newChannelForm()
	form.Name = "local"
	form.Dialect = "openai"

	baseURL := validateChannelBaseURL(&form)
	if err := baseURL("ftp://example.com"); err == nil {
		t.Error("非 http 基址应当报错")
	}
	if err := baseURL("https://example.com/v1"); err != nil {
		t.Errorf("基址带路径时交由 path 那一步把关，这里不该报错: %v", err)
	}

	form.BaseURL = "https://example.com/v1"
	path := validateChannelPath(&form)
	if err := path(""); err == nil {
		t.Error("基址带路径时 path 留空应当报错")
	}
	if err := path("/chat/completions"); err != nil {
		t.Errorf("补上 path 后不该报错: %v", err)
	}

	if err := validateChannelTimeout(""); err != nil {
		t.Errorf("留空超时用默认值，不该报错: %v", err)
	}
	if err := validateChannelTimeout("0s"); err == nil {
		t.Error("非正超时应当报错")
	}
	if err := validateChannelTimeout("abc"); err == nil {
		t.Error("无法解析的超时应当报错")
	}
	if err := validateChannelTimeout("30s"); err != nil {
		t.Errorf("合法超时不该报错: %v", err)
	}

	if err := validateChannelRetries(""); err != nil {
		t.Errorf("留空重试用 0，不该报错: %v", err)
	}
	if err := validateChannelRetries("11"); err == nil {
		t.Error("超过上限的重试应当报错")
	}
	if err := validateChannelRetries("-1"); err == nil {
		t.Error("负数重试应当报错")
	}
	if err := validateChannelRetries("x"); err == nil {
		t.Error("非整数重试应当报错")
	}
	if err := validateChannelRetries("3"); err != nil {
		t.Errorf("合法重试不该报错: %v", err)
	}

	form.AuthStyle = channel.StyleNone
	if err := validateChannelKeyEnv(&form)(""); err != nil {
		t.Errorf("不鉴权时 key_env 可留空: %v", err)
	}
	form.AuthStyle = channel.StyleBearer
	if err := validateChannelKeyEnv(&form)(""); err == nil {
		t.Error("bearer 缺 key_env 应当报错")
	}
	if err := validateChannelKeyEnv(&form)("K"); err != nil {
		t.Errorf("bearer 有 key_env 不该报错: %v", err)
	}
	form.AuthStyle = channel.StyleCustom
	if err := validateChannelHeader(&form)(""); err == nil {
		t.Error("custom 缺 header 应当报错")
	}
	form.AuthStyle = channel.StyleQuery
	if err := validateChannelQuery(&form)(""); err == nil {
		t.Error("query 缺参数名应当报错")
	}
}

// TestChannelDialectChoicesCoverCore 验证交互里的方言选项覆盖 core 的全部方言。
func TestChannelDialectChoicesCoverCore(t *testing.T) {
	seen := make(map[string]bool)
	for _, choice := range channelDialectChoices {
		if _, ok := dialect.Parse(choice.value); !ok {
			t.Errorf("方言选项 %q 不是 core 支持的取值", choice.value)
		}
		seen[choice.value] = true
	}
	for _, name := range dialect.Names() {
		if !seen[string(name)] {
			t.Errorf("方言选项里缺少 %q", name)
		}
	}
}

// TestChannelEditFields 验证编辑只比出变化的字段，清空的可选字段用 nil 删除。
func TestChannelEditFields(t *testing.T) {
	retries := 2
	current := config.Channel{
		Name:       "example",
		Dialect:    "openai",
		BaseURL:    "https://api.example.com",
		Path:       "/chat/completions",
		Auth:       config.Auth{Style: channel.StyleBearer, KeyEnv: "EXAMPLE_KEY"},
		Timeout:    config.Duration(channel.DefaultTimeout),
		MaxRetries: &retries,
	}

	// 原样不动时没有字段要写。
	if fields := channelEditFields(current, channelEntryOf(current)); len(fields) != 0 {
		t.Errorf("没有变化时不该有字段，实际 %d 个: %+v", len(fields), fields)
	}

	entry := channelEntryOf(current)
	entry.BaseURL = "https://api.changed.com"
	entry.Path = ""
	entry.Auth.KeyEnv = "CHANGED_KEY"
	entry.MaxRetries = nil

	fields := channelEditFields(current, entry)
	got := make(map[string]any)
	for _, field := range fields {
		got[strings.Join(field.Path, ".")] = field.Value
	}
	if got["base_url"] != "https://api.changed.com" {
		t.Errorf("base_url 应当变化: %+v", got)
	}
	if value, ok := got["path"]; !ok || value != nil {
		t.Errorf("清空的 path 应当用 nil 删除: %+v", got)
	}
	if got["auth.key_env"] != "CHANGED_KEY" {
		t.Errorf("key_env 应当变化: %+v", got)
	}
	if value, ok := got["max_retries"]; !ok || value != nil {
		t.Errorf("清零的 max_retries 应当用 nil 删除: %+v", got)
	}
	if _, ok := got["name"]; ok {
		t.Errorf("没变的 name 不该出现: %+v", got)
	}
}

// TestChannelEditFieldsNormalizesStyle 验证没写 style 的渠道不会被无故补出 style: none。
func TestChannelEditFieldsNormalizesStyle(t *testing.T) {
	current := config.Channel{Name: "example", Dialect: "openai", BaseURL: "https://api.example.com", Timeout: config.Duration(channel.DefaultTimeout)}
	entry := channelEntryOf(current)
	entry.Auth.Style = channel.StyleNone

	if fields := channelEditFields(current, entry); len(fields) != 0 {
		t.Errorf("空 style 与 none 等价，不该有字段: %+v", fields)
	}
}

// TestModelFormFromRuleKeepsCurrentValues 验证编辑规则时默认值是当前值。
func TestModelFormFromRuleKeepsCurrentValues(t *testing.T) {
	rule := config.Model{
		Match:    "deepseek-*",
		Channel:  "commandcode",
		Upstream: "deepseek/${model}",
		Dialect:  "claude",
		Affinity: "accounts",
	}
	want := modelForm{
		Match:    "deepseek-*",
		Channel:  "commandcode",
		Upstream: "deepseek/${model}",
		Dialect:  "claude",
		Affinity: "accounts",
	}
	if got := modelFormFromRule(rule); got != want {
		t.Errorf("编辑默认值不对\n--- got ---\n%+v\n--- want ---\n%+v", got, want)
	}
}

// TestModelRecordFromForm 验证规则组装与校验。
func TestModelRecordFromForm(t *testing.T) {
	cfg, err := config.Parse(strings.NewReader(validConfig))
	if err != nil {
		t.Fatalf("解析配置失败: %v", err)
	}

	form := modelForm{Match: "gpt-*", Channel: "example", Upstream: "gpt/${model}"}
	record, err := modelRecordFromForm(cfg, form)
	if err != nil {
		t.Fatalf("modelRecordFromForm: %v", err)
	}
	if record.Match != "gpt-*" || record.Channel != "example" || record.Upstream != "gpt/${model}" {
		t.Errorf("组装结果不对: %+v", record)
	}
	if record.Dialect != "" || record.Affinity != "" {
		t.Errorf("留空字段应当为空: %+v", record)
	}

	cases := []struct {
		name string
		form modelForm
	}{
		{"匹配式留空", modelForm{Match: "", Channel: "example"}},
		{"匹配式非法 UTF-8", modelForm{Match: "\xff", Channel: "example"}},
		{"渠道留空", modelForm{Match: "gpt-*", Channel: ""}},
		{"渠道不存在", modelForm{Match: "gpt-*", Channel: "absent"}},
		{"方言非法", modelForm{Match: "gpt-*", Channel: "example", Dialect: "nope"}},
		{"占位符不对", modelForm{Match: "gpt-*", Channel: "example", Upstream: "${foo}"}},
		{"占位符没有收尾", modelForm{Match: "gpt-*", Channel: "example", Upstream: "${model"}},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			if _, err := modelRecordFromForm(cfg, item.form); err == nil {
				t.Errorf("%s 应当报错", item.name)
			}
		})
	}
}

// TestModelDialectChoicesHasEmptyFirst 验证方言选项第一项是留空。
func TestModelDialectChoicesHasEmptyFirst(t *testing.T) {
	if len(modelDialectChoices) == 0 || modelDialectChoices[0].value != "" {
		t.Fatalf("方言选项第一项应当是留空: %+v", modelDialectChoices)
	}
	if len(modelDialectChoices) != len(channelDialectChoices)+1 {
		t.Errorf("方言选项数量不对: %d", len(modelDialectChoices))
	}
}

// TestValidateModelFields 验证 match、渠道与上游占位符的逐步校验。
func TestValidateModelFields(t *testing.T) {
	if err := validateModelMatch("  "); err == nil {
		t.Error("空匹配式应当报错")
	}
	if err := validateModelMatch("deepseek-*"); err != nil {
		t.Errorf("合法匹配式不该报错: %v", err)
	}

	if err := validateModelUpstream(""); err != nil {
		t.Errorf("留空上游不该报错: %v", err)
	}
	if err := validateModelUpstream("deepseek/${model}"); err != nil {
		t.Errorf("合法占位符不该报错: %v", err)
	}
	if err := validateModelUpstream("${foo}"); err == nil {
		t.Error("不支持的占位符应当报错")
	}
	if err := validateModelUpstream("${model"); err == nil {
		t.Error("没有收尾的占位符应当报错")
	}
}

// TestModelEditFields 验证规则编辑只比出变化字段。
func TestModelEditFields(t *testing.T) {
	current := config.Model{Match: "gpt-*", Channel: "example", Upstream: "gpt-4o", Dialect: "openai", Affinity: "a"}
	if fields := modelEditFields(current, modelEntryOf(current)); len(fields) != 0 {
		t.Errorf("没有变化时不该有字段: %+v", fields)
	}

	entry := modelEntryOf(current)
	entry.Match = "claude-*"
	entry.Upstream = ""
	entry.Affinity = ""

	got := make(map[string]any)
	for _, field := range modelEditFields(current, entry) {
		got[strings.Join(field.Path, ".")] = field.Value
	}
	if got["match"] != "claude-*" {
		t.Errorf("match 应当变化: %+v", got)
	}
	if value, ok := got["upstream"]; !ok || value != nil {
		t.Errorf("清空的上游应当用 nil 删除: %+v", got)
	}
	if value, ok := got["affinity"]; !ok || value != nil {
		t.Errorf("清空的亲和应当用 nil 删除: %+v", got)
	}
	if _, ok := got["channel"]; ok {
		t.Errorf("没变的渠道不该出现: %+v", got)
	}
}

// TestManageChannelCreateWrites 验证交互路径的新建写回与注释保留。
func TestManageChannelCreateWrites(t *testing.T) {
	path := configSandbox(t, emptyChannelsConfig)
	env, err := configmgr.Load(path)
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}

	form := newChannelForm()
	form.Name = "local"
	form.Dialect = "openai"
	form.BaseURL = "http://127.0.0.1:8080"
	record, err := channelRecordFromForm(form)
	if err != nil {
		t.Fatalf("组装渠道失败: %v", err)
	}
	if err := applyChannelCreate(env, channelEntryOf(record)); err != nil {
		t.Fatalf("applyChannelCreate: %v", err)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("改写后的配置不合法: %v", err)
	}
	if _, ok := cfg.ChannelByName("local"); !ok {
		t.Fatal("配置里没有新渠道")
	}
	data := readFile(t, path)
	for _, want := range []string{"- name: local", "style: none"} {
		if !strings.Contains(data, want) {
			t.Errorf("改写结果里没有 %q:\n%s", want, data)
		}
	}
	for _, unwanted := range []string{`path: ""`, "key_env"} {
		if strings.Contains(data, unwanted) {
			t.Errorf("不该写出 %q:\n%s", unwanted, data)
		}
	}
}

// TestManageChannelEditWrites 验证交互路径的编辑只写变化字段，注释与缩进保留。
func TestManageChannelEditWrites(t *testing.T) {
	path := configSandbox(t, editChannelConfig)
	env, err := configmgr.Load(path)
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}

	form := channelFormFromRecord(env.Config.Channels[0])
	form.BaseURL = "https://api.changed.com"
	form.Path = ""
	form.KeyEnv = "CHANGED_KEY"
	form.MaxRetries = ""
	record, err := channelRecordFromForm(form)
	if err != nil {
		t.Fatalf("组装渠道失败: %v", err)
	}
	entry := channelEntryOf(record)
	entry.Headers = env.Config.Channels[0].Headers
	if err := applyChannelEdit(env, 0, entry); err != nil {
		t.Fatalf("applyChannelEdit: %v", err)
	}

	data := readFile(t, path)
	for _, want := range []string{"# 主渠道", "# 凭据变量", "# 主规则", "base_url: https://api.changed.com", "key_env: CHANGED_KEY"} {
		if !strings.Contains(data, want) {
			t.Errorf("改写结果里没有 %q:\n%s", want, data)
		}
	}
	for _, unwanted := range []string{"path:", "max_retries"} {
		if strings.Contains(data, unwanted) {
			t.Errorf("清空的字段不该写出来 %q:\n%s", unwanted, data)
		}
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("改写后的配置不合法: %v", err)
	}
	channel, ok := cfg.ChannelByName("example")
	if !ok {
		t.Fatal("渠道不见了")
	}
	if channel.Path != "" || retriesOf(channel) != 0 {
		t.Errorf("清空的可选字段没有生效: %+v", channel)
	}
}

// TestManageChannelEditKeepsUnchanged 验证没有改动时不写文件。
func TestManageChannelEditKeepsUnchanged(t *testing.T) {
	path := configSandbox(t, editChannelConfig)
	env, err := configmgr.Load(path)
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}

	// editChannelConfig 的路径与基址配得合法，原样回车不应改动。
	form := channelFormFromRecord(env.Config.Channels[0])
	record, err := channelRecordFromForm(form)
	if err != nil {
		t.Fatalf("组装渠道失败: %v", err)
	}
	entry := channelEntryOf(record)
	entry.Headers = env.Config.Channels[0].Headers

	before := readFile(t, path)
	if err := applyChannelEdit(env, 0, entry); err != nil {
		t.Fatalf("applyChannelEdit: %v", err)
	}
	if readFile(t, path) != before {
		t.Error("没有改动时文件不该变")
	}
}

// TestManageChannelDeleteWrites 验证交互路径的删除与注释保留。
func TestManageChannelDeleteWrites(t *testing.T) {
	path := configSandbox(t, browseConfig)
	env, err := configmgr.Load(path)
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}

	// 第二条 backup 没有被规则引用。
	removed, err := removeChannelAt(env, 1)
	if err != nil {
		t.Fatalf("removeChannelAt: %v", err)
	}
	if removed != "backup" {
		t.Errorf("删掉的应当是 backup，实际是 %q", removed)
	}

	data := readFile(t, path)
	if strings.Contains(data, "backup") {
		t.Errorf("文件里不该还有 backup:\n%s", data)
	}
	for _, want := range []string{"# 主渠道，被规则引用", "# 主规则", "- name: primary"} {
		if !strings.Contains(data, want) {
			t.Errorf("改写结果里没有 %q:\n%s", want, data)
		}
	}
}

// TestManageChannelDeleteRefusesReferenced 验证交互路径同样拒绝删除被引用的渠道。
func TestManageChannelDeleteRefusesReferenced(t *testing.T) {
	path := configSandbox(t, browseConfig)
	env, err := configmgr.Load(path)
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}
	before := readFile(t, path)

	_, err = removeChannelAt(env, 0)
	if err == nil {
		t.Fatal("删除被引用的渠道应当报错")
	}
	for _, want := range []string{"仍被", "models[0]", "models[1]", "gpt-*"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("拒绝信息里应当包含 %q: %v", want, err)
		}
	}
	if readFile(t, path) != before {
		t.Error("被拒绝后文件不该改动")
	}
}

// TestManageChannelRenameRefusedWhenReferenced 验证被规则引用的渠道不能改名。
func TestManageChannelRenameRefusedWhenReferenced(t *testing.T) {
	cfg, err := config.Parse(strings.NewReader(browseConfig))
	if err != nil {
		t.Fatalf("解析配置失败: %v", err)
	}
	if err := checkChannelRename(cfg, "primary", "renamed"); err == nil {
		t.Fatal("被引用的渠道改名应当报错")
	} else if !strings.Contains(err.Error(), "仍被") {
		t.Errorf("错误信息应当说明被引用: %v", err)
	}
	if err := checkChannelRename(cfg, "backup", "renamed"); err != nil {
		t.Errorf("没有被引用的渠道可以改名: %v", err)
	}
}

// TestManageModelCreateWrites 验证交互路径新增规则。
func TestManageModelCreateWrites(t *testing.T) {
	path := configSandbox(t, editableConfig)
	env, err := configmgr.Load(path)
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}

	form := newModelForm(env.Config)
	form.Match = "deepseek-*"
	form.Upstream = "deepseek/${model}"
	record, err := modelRecordFromForm(env.Config, form)
	if err != nil {
		t.Fatalf("组装规则失败: %v", err)
	}
	if err := applyModelCreate(env, modelEntryOf(record)); err != nil {
		t.Fatalf("applyModelCreate: %v", err)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("改写后的配置不合法: %v", err)
	}
	if len(cfg.Models) != 2 {
		t.Fatalf("规则数量不对: %d", len(cfg.Models))
	}
	data := readFile(t, path)
	for _, want := range []string{"# 主规则", "- match: deepseek-*", "upstream: deepseek/${model}"} {
		if !strings.Contains(data, want) {
			t.Errorf("改写结果里没有 %q:\n%s", want, data)
		}
	}
}

// TestManageModelEditWrites 验证交互路径编辑规则。
func TestManageModelEditWrites(t *testing.T) {
	path := configSandbox(t, editableConfig)
	env, err := configmgr.Load(path)
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}

	form := modelFormFromRule(env.Config.Models[0])
	form.Upstream = "deepseek/${model}"
	form.Affinity = "accounts"
	record, err := modelRecordFromForm(env.Config, form)
	if err != nil {
		t.Fatalf("组装规则失败: %v", err)
	}
	if err := applyModelEdit(env, 0, modelEntryOf(record)); err != nil {
		t.Fatalf("applyModelEdit: %v", err)
	}

	data := readFile(t, path)
	for _, want := range []string{"# 主规则", "upstream: deepseek/${model}", "affinity: accounts", "channel: example"} {
		if !strings.Contains(data, want) {
			t.Errorf("改写结果里没有 %q:\n%s", want, data)
		}
	}
}

// TestManageModelDeleteWrites 验证交互路径删除规则。
func TestManageModelDeleteWrites(t *testing.T) {
	path := configSandbox(t, browseConfig)
	env, err := configmgr.Load(path)
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}

	removed, err := removeModelAt(env, 1)
	if err != nil {
		t.Fatalf("removeModelAt: %v", err)
	}
	if removed.Match != "claude-*" {
		t.Errorf("删掉的应当是 claude-*，实际是 %q", removed.Match)
	}

	data := readFile(t, path)
	if strings.Contains(data, "claude-*") {
		t.Errorf("文件里不该还有 claude-*:\n%s", data)
	}
	for _, want := range []string{"# 主规则", "gpt-*", "- name: primary"} {
		if !strings.Contains(data, want) {
			t.Errorf("改写结果里没有 %q:\n%s", want, data)
		}
	}
}

// TestManageItemsMatchRows 验证交互条目的数量与标签顺序与列表一致。
func TestManageItemsMatchRows(t *testing.T) {
	env, err := configmgr.Load(configSandbox(t, browseConfig))
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}

	channels := channelBrowseItems(env)
	if len(channels) != len(env.Config.Channels) {
		t.Fatalf("渠道条目数量不对: %d", len(channels))
	}
	if channels[1].label != "backup" {
		t.Errorf("渠道条目顺序不对: %+v", channels)
	}
	if len(channels[1].detail) != 13 {
		t.Errorf("渠道详情应当有 13 个字段: %d", len(channels[1].detail))
	}
	labels := make(map[string]string, len(channels[1].detail))
	for _, field := range channels[1].detail {
		labels[field[0]] = field[1]
	}
	// 嵌套的 auth 要展开成四个子字段，而不是揉成一行缩写。
	for _, want := range []string{"鉴权方式", "凭据环境变量", "引用账号", "请求头名", "查询参数名"} {
		if _, ok := labels[want]; !ok {
			t.Errorf("渠道详情里缺少字段 %q: %+v", want, channels[1].detail)
		}
	}

	rules := modelBrowseItems(env)
	if len(rules) != len(env.Config.Models) {
		t.Fatalf("规则条目数量不对: %d", len(rules))
	}
	if !strings.Contains(rules[0].label, "gpt-*") {
		t.Errorf("规则标签应当带上匹配式: %q", rules[0].label)
	}
	if len(rules[0].detail) != 5 {
		t.Errorf("规则详情应当有 5 个字段: %d", len(rules[0].detail))
	}
}

// TestManageCancelledErrorIsSentinel 验证取消错误可以被识别。
func TestManageCancelledErrorIsSentinel(t *testing.T) {
	if !strings.Contains(errManageCancelled.Error(), "取消") {
		t.Errorf("取消错误信息应当写明: %v", errManageCancelled)
	}
}

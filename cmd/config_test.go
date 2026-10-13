package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tokenmp/core/config"

	"github.com/tokenmp/cli/internal/configmgr"
)

// validConfig 是一份能通过校验的配置，字段故意写得比默认值少，好验证默认值的填充。
const validConfig = `# 沙箱配置
listen: 127.0.0.1:9999

log:
  format: text

channels:
  - name: example
    dialect: openai
    base_url: https://api.example.com/v1
    auth:
      style: bearer
      key_env: TOKENMP_TEST_KEY
    # 超时留空，由默认值填

models:
  - match: "gpt-*"
    channel: example

plugins:
  dir: plugins
`

// configSandbox 写一份配置并返回它的路径。
func configSandbox(t *testing.T, content string) string {
	t.Helper()

	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	writeFile(t, path, content)
	return path
}

func TestConfigInitThenValidate(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "nested", "config.yaml")

	out, err := run(t, "config", "init", "--config", path)
	if err != nil {
		t.Fatalf("config init: %v\n%s", err, out)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("配置文件没有生成: %v", err)
	}

	// 生成的骨架本身必须能通过校验。
	checked, err := run(t, "config", "validate", "--config", path)
	if err != nil {
		t.Fatalf("生成的配置应当合法: %v\n%s", err, checked)
	}
	if !strings.Contains(checked, "合法") {
		t.Errorf("校验输出不对:\n%s", checked)
	}

	if _, err := run(t, "config", "init", "--config", path); err == nil {
		t.Fatal("配置文件已存在时应当报错")
	}
	if _, err := run(t, "config", "init", "--config", path, "--force"); err != nil {
		t.Fatalf("--force 应当允许覆盖: %v", err)
	}
}

func TestConfigShowOverview(t *testing.T) {
	path := configSandbox(t, validConfig)

	// 测试里输出是缓冲区，不是终端，因此默认格式退化成纯文本。
	out, err := run(t, "config", "show", "--config", path)
	if err != nil {
		t.Fatalf("config show: %v", err)
	}

	plain, err := run(t, "config", "show", "--config", path, "--format", "yaml")
	if err != nil {
		t.Fatalf("config show --format yaml: %v", err)
	}
	if out != plain {
		t.Errorf("不是终端时默认格式应当与 --format yaml 一致\n默认:\n%s\nyaml:\n%s", out, plain)
	}
	for _, want := range []string{"127.0.0.1:9999", "example", "gpt-*"} {
		if !strings.Contains(out, want) {
			t.Errorf("输出里没有 %q:\n%s", want, out)
		}
	}
}

func TestConfigShowYAML(t *testing.T) {
	path := configSandbox(t, validConfig)

	out, err := run(t, "config", "show", "--config", path, "--format", "yaml")
	if err != nil {
		t.Fatalf("config show --format yaml: %v", err)
	}
	for _, want := range []string{"127.0.0.1:9999", "timeout: 2m0s", "max_retries: 0", "dir: " + filepath.Join(filepath.Dir(path), "plugins")} {
		if !strings.Contains(out, want) {
			t.Errorf("YAML 里没有 %q:\n%s", want, out)
		}
	}
	// 空值字段不打印。
	for _, unwanted := range []string{`path: ""`, `upstream: ""`, "list: []", "headers: {}"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("YAML 里不该出现 %q:\n%s", unwanted, out)
		}
	}
}

func TestConfigShowJSON(t *testing.T) {
	path := configSandbox(t, validConfig)

	// --json 是整组通用的开关，与 --format json 等价。
	for _, extra := range [][]string{{"--format", "json"}, {"--json"}} {
		args := append([]string{"config", "show", "--config", path}, extra...)
		out, err := run(t, args...)
		if err != nil {
			t.Fatalf("config show %v: %v", extra, err)
		}

		var generic map[string]any
		if err := json.Unmarshal([]byte(out), &generic); err != nil {
			t.Fatalf("输出不是合法 JSON: %v\n%s", err, out)
		}
		if generic["listen"] != "127.0.0.1:9999" {
			t.Errorf("listen 不对: %v", generic["listen"])
		}
		channels, ok := generic["channels"].([]any)
		if !ok || len(channels) != 1 {
			t.Fatalf("channels 不对: %v", generic["channels"])
		}
		first, ok := channels[0].(map[string]any)
		if !ok || first["dialect"] != "openai" {
			t.Errorf("渠道字段不对: %v", channels[0])
		}
	}
}

func TestConfigShowRejectsUnknownFormat(t *testing.T) {
	path := configSandbox(t, validConfig)

	out, err := run(t, "config", "show", "--config", path, "--format", "bogus")
	if err == nil {
		t.Fatal("未知格式应当报错")
	}
	if !strings.Contains(out, "ui") || !strings.Contains(out, "yaml") {
		t.Errorf("错误信息里应当列出可用格式:\n%s", out)
	}
}

func TestConfigGet(t *testing.T) {
	path := configSandbox(t, validConfig)

	cases := map[string]string{
		"listen":             "127.0.0.1:9999",
		"log.level":          "info",
		"channels.0.name":    "example",
		"plugins.dir":        filepath.Join(filepath.Dir(path), "plugins"),
		"channels.0.timeout": "2m0s",
	}
	for target, want := range cases {
		out, err := run(t, "config", "get", target, "--config", path)
		if err != nil {
			t.Fatalf("config get %s: %v", target, err)
		}
		if !strings.Contains(out, want) {
			t.Errorf("config get %s 得到 %q，期望包含 %q", target, out, want)
		}
	}

	if _, err := run(t, "config", "get", "nope", "--config", path); err == nil {
		t.Error("取不存在的路径应当报错")
	}
	if _, err := run(t, "config", "get", "channels.9.name", "--config", path); err == nil {
		t.Error("下标越界应当报错")
	}
}

func TestConfigSetWritesAndKeepsComments(t *testing.T) {
	path := configSandbox(t, validConfig)

	if _, err := run(t, "config", "set", "log.level", "debug", "--config", path); err != nil {
		t.Fatalf("config set: %v", err)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("改写后的配置不合法: %v", err)
	}
	if cfg.Log.Level != "debug" {
		t.Errorf("log.level 应当是 debug，实际 %q", cfg.Log.Level)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读配置失败: %v", err)
	}
	for _, want := range []string{"# 沙箱配置", "# 超时留空，由默认值填"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("注释 %q 丢了:\n%s", want, data)
		}
	}
}

func TestConfigSetRollsBackInvalidValue(t *testing.T) {
	path := configSandbox(t, validConfig)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读配置失败: %v", err)
	}

	if _, err := run(t, "config", "set", "log.level", "bogus", "--config", path); err == nil {
		t.Fatal("非法取值应当报错")
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读配置失败: %v", err)
	}
	if string(before) != string(after) {
		t.Errorf("非法取值应当还原原文件\n改写前:\n%s\n改写后:\n%s", before, after)
	}
}

func TestConfigSetRejectsSectionsAndUnknownPaths(t *testing.T) {
	path := configSandbox(t, validConfig)

	for _, target := range []string{"channels", "channels.0", "nope", "log"} {
		if _, err := run(t, "config", "set", target, "x", "--config", path); err == nil {
			t.Errorf("改写 %s 应当被拒绝", target)
		}
	}

	// 不在白名单里的路径仍然被拒绝，报错里要能看出哪些路径可以直接补。
	for _, target := range []string{"nope", "log.nope"} {
		out, err := run(t, "config", "set", target, "x", "--config", path)
		if err == nil {
			t.Errorf("改写 %s 应当被拒绝:\n%s", target, out)
		}
		for _, want := range append([]string{"listen", "plugins.dir"}, configmgr.OtherScalarPaths()...) {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s 的报错里应当列出可改写的路径，缺 %q: %v", target, want, err)
			}
		}
	}
}

// TestConfigSetScalarsOutsideWhitelist 白名单之外的顶层标量照样能改，不必先在文件里写上
// 一行：白名单只在「文件里整个段落都没有」时起作用 —— 那些字段解出来是空，路径取不到值，
// 得靠白名单放行。defaults.max_body_bytes 这种带父段落的键，父段落即使没写也在结构树里，
// 路径本就取得到值，因此直接可改。
func TestConfigSetScalarsOutsideWhitelist(t *testing.T) {
	const key = "defaults.max_body_bytes"
	allowed := map[string]bool{}
	for _, path := range configmgr.SettablePaths {
		allowed[path] = true
	}
	if allowed[key] {
		t.Fatalf("%s 不该在白名单里，这条用例就失去意义了", key)
	}

	path := configSandbox(t, validConfig)
	if _, err := run(t, "config", "set", key, "1048576", "--config", path); err != nil {
		t.Fatalf("白名单之外的顶层标量应当能直接改: %v", err)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("改写后的配置不合法: %v", err)
	}
	if cfg.Behavior.MaxBodyBytes == nil || *cfg.Behavior.MaxBodyBytes != 1048576 {
		t.Errorf("max_body_bytes 是 %v，期望 1048576", cfg.Behavior.MaxBodyBytes)
	}
}

// TestConfigSetScalarListFollowsKeys set 的帮助文案里的路径名由 core 的键表现算，
// 不再手抄一份：白名单与「其余顶层标量」两份加起来正是键表里的顶层标量。
func TestConfigSetScalarListFollowsKeys(t *testing.T) {
	out, err := run(t, "config", "set", "--help")
	if err != nil {
		t.Fatalf("config set --help: %v\n%s", err, out)
	}
	for _, want := range append(append([]string{}, configmgr.SettablePaths...), configmgr.OtherScalarPaths()...) {
		if !strings.Contains(out, want) {
			t.Errorf("帮助文案里没有顶层标量 %s:\n%s", want, out)
		}
	}
	// 白名单之外的清单由键表减白名单现算，不该出现键表里没有的路径。
	for _, key := range configmgr.ScalarKeys() {
		if !strings.Contains(out, key.Path) {
			t.Errorf("键表里的顶层标量 %s 没有出现在帮助文案里:\n%s", key.Path, out)
		}
	}
}

// TestConfigSetWritesScalarTypes 改写按 core 键表里的类型编码：整数字段落盘成数字，
// 时长段照旧写字符串，两者写回后都能重新加载。
func TestConfigSetWritesScalarTypes(t *testing.T) {
	path := configSandbox(t, validConfig+`
defaults:
  max_body_bytes: 1048576
`)

	if _, err := run(t, "config", "set", "defaults.max_body_bytes", "2097152", "--config", path); err != nil {
		t.Fatalf("config set defaults.max_body_bytes: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读配置失败: %v", err)
	}
	if !strings.Contains(string(data), "max_body_bytes: 2097152") {
		t.Errorf("整数要写成数字而不是字符串:\n%s", data)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("改写后的配置不合法: %v", err)
	}
	if cfg.Behavior.MaxBodyBytes == nil || *cfg.Behavior.MaxBodyBytes != 2097152 {
		t.Errorf("max_body_bytes 是 %v，期望 2097152", cfg.Behavior.MaxBodyBytes)
	}

	// 非整数的取值在写盘之前就被拦下，文件不动。
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读配置失败: %v", err)
	}
	if _, err := run(t, "config", "set", "defaults.max_body_bytes", "1MiB", "--config", path); err == nil {
		t.Fatal("整数字段给 1MiB 应当被拒绝")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读配置失败: %v", err)
	}
	if string(before) != string(after) {
		t.Errorf("取值不合法时不该动文件\n改写前:\n%s\n改写后:\n%s", before, after)
	}
}

func TestConfigSetRequiresExistingFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "missing.yaml")

	out, err := run(t, "config", "set", "listen", "127.0.0.1:1", "--config", path)
	if err == nil {
		t.Fatal("配置文件不存在时 set 应当报错")
	}
	if !strings.Contains(out, "config init") {
		t.Errorf("错误信息里应当提示 config init:\n%s", out)
	}
}

func TestConfigValidateReportsProblems(t *testing.T) {
	path := configSandbox(t, `listen: 127.0.0.1:8787
channels:
  - name: example
    dialect: not-a-dialect
    base_url: https://api.example.com/v1
models:
  - match: "gpt-*"
    channel: missing
`)

	out, err := run(t, "config", "validate", "--config", path)
	if err == nil {
		t.Fatal("不合法的配置应当让 validate 失败")
	}
	for _, want := range []string{"not-a-dialect", "missing"} {
		if !strings.Contains(out, want) {
			t.Errorf("校验结果里没有 %q:\n%s", want, out)
		}
	}
}

func TestConfigValidateMissingFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "missing.yaml")

	if _, err := run(t, "config", "validate", "--config", path); err == nil {
		t.Fatal("配置文件不存在时 validate 应当失败")
	}
}

func TestChannelsList(t *testing.T) {
	path := configSandbox(t, validConfig)

	out, err := run(t, "channels", "list", "--config", path)
	if err != nil {
		t.Fatalf("channels list: %v", err)
	}
	for _, want := range []string{"example", "openai", "bearer ← TOKENMP_TEST_KEY", credentialMissing} {
		if !strings.Contains(out, want) {
			t.Errorf("渠道表里没有 %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "TOKENMP_TEST_KEY=secret") {
		t.Error("输出里不该出现凭据的值")
	}

	t.Setenv("TOKENMP_TEST_KEY", "secret")
	out, err = run(t, "channels", "list", "--config", path)
	if err != nil {
		t.Fatalf("channels list: %v", err)
	}
	if !strings.Contains(out, credentialPresent) {
		t.Errorf("设了环境变量之后应当报告已设置:\n%s", out)
	}
	if strings.Contains(out, "secret") {
		t.Error("输出里不该出现凭据的值")
	}
}

func TestChannelsListJSON(t *testing.T) {
	path := configSandbox(t, validConfig)

	out, err := run(t, "channels", "list", "--config", path, "--json")
	if err != nil {
		t.Fatalf("channels list --json: %v", err)
	}

	var rows []channelRow
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("输出不是合法 JSON: %v\n%s", err, out)
	}
	if len(rows) != 1 || rows[0].Name != "example" || rows[0].Timeout != "2m0s" {
		t.Errorf("渠道行不对: %+v", rows)
	}
}

func TestModelsList(t *testing.T) {
	path := configSandbox(t, validConfig)

	out, err := run(t, "models", "list", "--config", path)
	if err != nil {
		t.Fatalf("models list: %v", err)
	}
	for _, want := range []string{"gpt-*", "example", "同请求的模型名", "openai（同渠道）"} {
		if !strings.Contains(out, want) {
			t.Errorf("规则表里没有 %q:\n%s", want, out)
		}
	}

	resolved, err := run(t, "models", "list", "--model", "gpt-4o", "--config", path)
	if err != nil {
		t.Fatalf("models list --model: %v", err)
	}
	for _, want := range []string{"候选路由", "gpt-4o"} {
		if !strings.Contains(resolved, want) {
			t.Errorf("试算结果里没有 %q:\n%s", want, resolved)
		}
	}

	unmatched, err := run(t, "models", "list", "--model", "llama-3", "--config", path)
	if err != nil {
		t.Fatalf("models list --model: %v", err)
	}
	if !strings.Contains(unmatched, "没有匹配的规则") {
		t.Errorf("没匹配时应当明说:\n%s", unmatched)
	}
}

func TestModelsListJSON(t *testing.T) {
	path := configSandbox(t, validConfig)

	out, err := run(t, "models", "list", "--model", "gpt-4o", "--config", path, "--json")
	if err != nil {
		t.Fatalf("models list --json: %v", err)
	}

	var result modelsOutput
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("输出不是合法 JSON: %v\n%s", err, out)
	}
	if len(result.Rules) != 1 || len(result.Routes) != 1 {
		t.Fatalf("规则或路由数量不对: %+v", result)
	}
	if result.Rules[0].Index != 0 || result.Rules[0].Match != "gpt-*" {
		t.Errorf("规则里的序号或匹配式不对: %+v", result.Rules[0])
	}
	if result.Routes[0].Upstream != "gpt-4o" || result.Routes[0].Dialect != "openai" {
		t.Errorf("候选路由不对: %+v", result.Routes[0])
	}
}

// affinityConfig 带上 ${model} 占位符与亲和组。
const affinityConfig = `listen: 127.0.0.1:9999

channels:
  - name: account-a
    dialect: openai
    base_url: https://api.example.com/v1
    path: /chat/completions
    auth:
      style: none

  - name: account-b
    dialect: openai
    base_url: https://api.example.com/v1
    path: /chat/completions
    auth:
      style: none

models:
  - match: "deepseek-*"
    channel: account-a
    upstream: "deepseek/${model}"
    affinity: accounts

  - match: "deepseek-*"
    channel: account-b
    affinity: accounts
`

func TestModelsListPlaceholderAndAffinity(t *testing.T) {
	path := configSandbox(t, affinityConfig)

	rules, err := run(t, "models", "list", "--config", path)
	if err != nil {
		t.Fatalf("models list: %v\n%s", err, rules)
	}
	// 规则表给出配置里的原样写法，亲和组名与 0 起的序号都在表里。
	for _, want := range []string{"deepseek/${model}", "同请求的模型名", "accounts", "序号"} {
		if !strings.Contains(rules, want) {
			t.Errorf("规则表里没有 %q:\n%s", want, rules)
		}
	}

	trial, err := run(t, "models", "list", "--config", path, "--model", "deepseek-chat")
	if err != nil {
		t.Fatalf("models list --model: %v\n%s", err, trial)
	}
	// 试算给出替换之后的名字：deepseek/${model} 配上 deepseek-chat。
	if !strings.Contains(trial, "deepseek/deepseek-chat") {
		t.Errorf("试算没有替换占位符:\n%s", trial)
	}

	out, err := run(t, "models", "list", "--config", path, "--model", "deepseek-chat", "--json")
	if err != nil {
		t.Fatalf("models list --json: %v\n%s", err, out)
	}
	var result modelsOutput
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("输出不是合法 JSON: %v\n%s", err, out)
	}
	if len(result.Routes) != 2 {
		t.Fatalf("候选路由数量不对: %+v", result.Routes)
	}
	if result.Routes[0].Upstream != "deepseek/deepseek-chat" || result.Routes[0].Affinity != "accounts" {
		t.Errorf("第一条候选路由不对: %+v", result.Routes[0])
	}
	if result.Routes[1].Upstream != "deepseek-chat" {
		t.Errorf("第二条候选路由应当沿用请求里的模型名: %+v", result.Routes[1])
	}
}

// poolConfig 用来验证试算里的亲和说明。组名取第一条带组名的候选，同组候选组成
// 一个池，池里不足两条时运行时不会重排，试算也就不该加说明。
const poolConfig = `listen: 127.0.0.1:9999

channels:
  - {name: account-a, dialect: openai, base_url: "https://api.example.com/v1", path: /chat/completions, auth: {style: none}}
  - {name: account-b, dialect: openai, base_url: "https://api.example.com/v1", path: /chat/completions, auth: {style: none}}

models:
  - {match: "shared-*", channel: account-a, affinity: accounts}
  - {match: "shared-*", channel: account-b, affinity: accounts}
  - {match: "solo-*", channel: account-a, affinity: accounts}
  - {match: "mixed-*", channel: account-a, affinity: solo}
  - {match: "mixed-*", channel: account-b, affinity: accounts}
  - {match: "mixed-*", channel: account-a, affinity: accounts}
  - {match: "plain-*", channel: account-a}
`

func TestModelsListAffinityNote(t *testing.T) {
	path := configSandbox(t, poolConfig)

	trial := func(model string) string {
		t.Helper()
		out, err := run(t, "models", "list", "--config", path, "--model", model)
		if err != nil {
			t.Fatalf("models list --model %s: %v\n%s", model, err, out)
		}
		return out
	}

	// 池里两条：运行时会按会话标识重排，试算要说清上表是配置顺序。
	if out := trial("shared-x"); !strings.Contains(out, "亲和组 accounts 的 2 条候选在运行时按会话标识重排") {
		t.Errorf("池里两条时应当加一句说明:\n%s", out)
	}

	// 池里只有一条：亲和没有可分配的对象，运行时不重排，不加说明。
	if out := trial("solo-x"); strings.Contains(out, "重排") {
		t.Errorf("池里一条时不该加说明:\n%s", out)
	}

	// 两个组名时只有排在最前的那一组组成池。这里它只有一条，因此不加说明。
	if out := trial("mixed-x"); strings.Contains(out, "重排") {
		t.Errorf("只认排在最前的组，它只有一条时不该加说明:\n%s", out)
	}

	// 一条候选都没写亲和时，输出与没有这个功能时一样。
	if out := trial("plain-x"); strings.Contains(out, "重排") {
		t.Errorf("没写亲和时不该加说明:\n%s", out)
	}
}

func TestConfigPath(t *testing.T) {
	path := configSandbox(t, validConfig)

	out, err := run(t, "config", "path", "--config", path)
	if err != nil {
		t.Fatalf("config path: %v", err)
	}
	// 路径、状态与每一个顶层标量都在字段表里，标量一栏跟着 core 的键表走。
	wants := []string{path, "已存在", "listen:", "log.format:", "log.level:", "plugins.dir:"}
	for _, key := range configmgr.ScalarPaths() {
		wants = append(wants, key+":")
	}
	for _, want := range wants {
		if !strings.Contains(out, want) {
			t.Errorf("输出里没有 %q:\n%s", want, out)
		}
	}

	missing := filepath.Join(t.TempDir(), "missing.yaml")
	out, err = run(t, "config", "path", "--config", missing, "--json")
	if err != nil {
		t.Fatalf("配置文件不存在时 config path 也该成功: %v", err)
	}

	var result configPathOutput
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("输出不是合法 JSON: %v\n%s", err, out)
	}
	if result.Exists {
		t.Error("不存在的配置文件应当报告 exists=false")
	}
	// 文件不存在时每个顶层标量都还在，取值是各自的默认值。
	for _, key := range configmgr.ScalarPaths() {
		if _, ok := result.Scalars[key]; !ok {
			t.Errorf("--json 里缺顶层标量 %s: %s", key, out)
		}
	}
	if result.Scalars["listen"] != "127.0.0.1:8787" {
		t.Errorf("默认 listen 不对: %v", result.Scalars["listen"])
	}
}

// TestConfigPathFollowsKeys 顶层标量的输出由 core 的键表现算：core 加减一个顶层标量时
// 字段表与 --json 都跟着变，这条盯着两边不漂。
func TestConfigPathFollowsKeys(t *testing.T) {
	path := configSandbox(t, validConfig)
	out, err := run(t, "config", "path", "--config", path, "--json")
	if err != nil {
		t.Fatalf("config path --json: %v\n%s", err, out)
	}

	var result configPathOutput
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("输出不是合法 JSON: %v\n%s", err, out)
	}
	if len(result.Scalars) != len(configmgr.ScalarKeys()) {
		t.Errorf("--json 里有 %d 项顶层标量，键表里有 %d 项: %s",
			len(result.Scalars), len(configmgr.ScalarKeys()), out)
	}
	for _, key := range configmgr.ScalarKeys() {
		if _, ok := result.Scalars[key.Path]; !ok {
			t.Errorf("键表里的 %s（类型 %s）没有出现在输出里: %s", key.Path, key.Type, out)
		}
	}
	// 文件里写了的项给出写下的取值，没写的给出填过默认值之后的取值。
	for _, want := range []string{`"listen": "127.0.0.1:9999"`, `"log.format": "text"`, `"log.level": "info"`} {
		if !strings.Contains(out, want) {
			t.Errorf("--json 里没有 %s:\n%s", want, out)
		}
	}
}

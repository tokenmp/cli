package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tokenmp/core/config"
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

	out, err := run(t, "config", "show", "--config", path)
	if err != nil {
		t.Fatalf("config show: %v", err)
	}
	for _, want := range []string{"服务", "渠道", "模型路由", "插件目录", "插件清单", "127.0.0.1:9999", "2m0s", "example", "openai"} {
		if !strings.Contains(out, want) {
			t.Errorf("概览视图里没有 %q:\n%s", want, out)
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

func TestConfigChannels(t *testing.T) {
	path := configSandbox(t, validConfig)

	out, err := run(t, "config", "channels", "--config", path)
	if err != nil {
		t.Fatalf("config channels: %v", err)
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
	out, err = run(t, "config", "channels", "--config", path)
	if err != nil {
		t.Fatalf("config channels: %v", err)
	}
	if !strings.Contains(out, credentialPresent) {
		t.Errorf("设了环境变量之后应当报告已设置:\n%s", out)
	}
	if strings.Contains(out, "secret") {
		t.Error("输出里不该出现凭据的值")
	}
}

func TestConfigChannelsJSON(t *testing.T) {
	path := configSandbox(t, validConfig)

	out, err := run(t, "config", "channels", "--config", path, "--json")
	if err != nil {
		t.Fatalf("config channels --json: %v", err)
	}

	var rows []channelRow
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("输出不是合法 JSON: %v\n%s", err, out)
	}
	if len(rows) != 1 || rows[0].Name != "example" || rows[0].Timeout != "2m0s" {
		t.Errorf("渠道行不对: %+v", rows)
	}
}

func TestConfigModels(t *testing.T) {
	path := configSandbox(t, validConfig)

	out, err := run(t, "config", "models", "--config", path)
	if err != nil {
		t.Fatalf("config models: %v", err)
	}
	for _, want := range []string{"gpt-*", "example", "同请求的模型名", "openai（同渠道）"} {
		if !strings.Contains(out, want) {
			t.Errorf("规则表里没有 %q:\n%s", want, out)
		}
	}

	resolved, err := run(t, "config", "models", "--model", "gpt-4o", "--config", path)
	if err != nil {
		t.Fatalf("config models --model: %v", err)
	}
	for _, want := range []string{"候选路由", "gpt-4o"} {
		if !strings.Contains(resolved, want) {
			t.Errorf("试算结果里没有 %q:\n%s", want, resolved)
		}
	}

	unmatched, err := run(t, "config", "models", "--model", "llama-3", "--config", path)
	if err != nil {
		t.Fatalf("config models --model: %v", err)
	}
	if !strings.Contains(unmatched, "没有匹配的规则") {
		t.Errorf("没匹配时应当明说:\n%s", unmatched)
	}
}

func TestConfigModelsJSON(t *testing.T) {
	path := configSandbox(t, validConfig)

	out, err := run(t, "config", "models", "--model", "gpt-4o", "--config", path, "--json")
	if err != nil {
		t.Fatalf("config models --json: %v", err)
	}

	var result modelsOutput
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("输出不是合法 JSON: %v\n%s", err, out)
	}
	if len(result.Rules) != 1 || len(result.Routes) != 1 {
		t.Fatalf("规则或路由数量不对: %+v", result)
	}
	if result.Routes[0].Upstream != "gpt-4o" || result.Routes[0].Dialect != "openai" {
		t.Errorf("候选路由不对: %+v", result.Routes[0])
	}
}

func TestConfigPath(t *testing.T) {
	path := configSandbox(t, validConfig)

	out, err := run(t, "config", "path", "--config", path)
	if err != nil {
		t.Fatalf("config path: %v", err)
	}
	for _, want := range []string{path, "已存在"} {
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
	if result.Listen != "127.0.0.1:8787" {
		t.Errorf("默认 listen 不对: %q", result.Listen)
	}
}

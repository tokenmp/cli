package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tokenmp/core/config"
	"github.com/tokenmp/core/plugin"

	"github.com/tokenmp/cli/internal/pluginmgr"
)

// echoSource 是一个可用插件：把配置里的 tag 写进请求体。
const echoSource = `export const meta = {
  key: "echo",
  name: "回显",
  version: "0.1.0",
  apiVersion: "v1",
  description: "把配置里的 tag 写进请求体",
};

export function onRequest(input) {
  input.body.tag = host.config.tag;
  return { body: input.body };
}
`

// echoFixture 是配套的 fixture，一条通过的用例。
const echoFixture = `{
  "unixNow": 1735689600,
  "cases": [
    {
      "name": "写入标记",
      "hook": "onRequest",
      "args": [{ "dialect": "openai", "model": "m", "stream": false, "body": {} }],
      "path": "body.tag",
      "expected": "sandbox"
    }
  ]
}
`

// sandboxConfig 带注释与缩进，用来验证改写不会把注释丢掉。
const sandboxConfig = `# 沙箱配置
listen: 127.0.0.1:8787

plugins:
  dir: plugins
  list:
    # 回显插件
    - key: echo
      enabled: false
      order: 10
      config:
        tag: sandbox
`

// sandbox 是一次测试用到的临时目录。
type sandbox struct {
	// root 是临时目录。
	root string
	// configPath 是配置文件路径。
	configPath string
	// pluginDir 是插件目录。
	pluginDir string
}

// newSandbox 造一份配置、一个可用插件与它的 fixture。
func newSandbox(t *testing.T, configText string) sandbox {
	t.Helper()

	root := t.TempDir()
	pluginDir := filepath.Join(root, "plugins")
	configPath := filepath.Join(root, "config.yaml")

	writeFile(t, filepath.Join(pluginDir, "echo", "plugin.js"), echoSource)
	writeFile(t, filepath.Join(pluginDir, "echo", "fixture.json"), echoFixture)
	writeFile(t, configPath, configText)

	return sandbox{root: root, configPath: configPath, pluginDir: pluginDir}
}

// writeFile 写一个文件，父目录不存在时创建。
func writeFile(t *testing.T, path, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("创建目录失败: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("写入 %s 失败: %v", path, err)
	}
}

func TestPluginListShowsState(t *testing.T) {
	box := newSandbox(t, sandboxConfig)

	out, err := run(t, "plugin", "list", "--config", box.configPath)
	if err != nil {
		t.Fatalf("plugin list: %v", err)
	}
	for _, want := range []string{"echo", "回显", "0.1.0", "onRequest", "停用"} {
		if !strings.Contains(out, want) {
			t.Errorf("列表里没有 %q:\n%s", want, out)
		}
	}
}

func TestPluginListJSON(t *testing.T) {
	box := newSandbox(t, sandboxConfig)

	out, err := run(t, "plugin", "list", "--config", box.configPath, "--json")
	if err != nil {
		t.Fatalf("plugin list --json: %v", err)
	}

	var summaries []pluginmgr.Summary
	if err := json.Unmarshal([]byte(out), &summaries); err != nil {
		t.Fatalf("输出不是合法 JSON: %v\n%s", err, out)
	}
	if len(summaries) != 1 {
		t.Fatalf("期望 1 个插件，实际 %d 个", len(summaries))
	}
	summary := summaries[0]
	if summary.Key != "echo" || summary.Name != "回显" || summary.Enabled || !summary.Registered {
		t.Errorf("插件摘要不对: %+v", summary)
	}
	if len(summary.Hooks) != 1 || summary.Hooks[0] != plugin.HookRequest {
		t.Errorf("钩子不对: %v", summary.Hooks)
	}
}

func TestPluginListKeepsGoingWithBrokenPlugin(t *testing.T) {
	box := newSandbox(t, sandboxConfig)
	writeFile(t, filepath.Join(box.pluginDir, "broken", "plugin.js"), "export const meta = {")

	out, err := run(t, "plugin", "list", "--config", box.configPath)
	if err != nil {
		t.Fatalf("坏插件不该让列表失败: %v", err)
	}
	for _, want := range []string{"echo", "broken", "编译失败"} {
		if !strings.Contains(out, want) {
			t.Errorf("列表里没有 %q:\n%s", want, out)
		}
	}
}

func TestPluginEnableWritesConfigAndKeepsComments(t *testing.T) {
	box := newSandbox(t, sandboxConfig)

	if _, err := run(t, "plugin", "enable", "echo", "--config", box.configPath); err != nil {
		t.Fatalf("plugin enable: %v", err)
	}

	cfg, err := config.Load(box.configPath)
	if err != nil {
		t.Fatalf("改写后的配置不合法: %v", err)
	}
	enabled := cfg.EnabledPlugins()
	if len(enabled) != 1 || enabled[0].Key != "echo" {
		t.Fatalf("echo 应当启用，实际 %+v", cfg.Plugins.List)
	}
	if enabled[0].Order != 10 {
		t.Errorf("顺序应当保持 10，实际 %d", enabled[0].Order)
	}
	if tag, _ := enabled[0].Config["tag"].(string); tag != "sandbox" {
		t.Errorf("config 段应当保持，实际 %v", enabled[0].Config)
	}

	data, err := os.ReadFile(box.configPath)
	if err != nil {
		t.Fatalf("读配置失败: %v", err)
	}
	for _, want := range []string{"# 沙箱配置", "# 回显插件"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("注释 %q 丢了:\n%s", want, data)
		}
	}
}

func TestPluginDisableAndOrder(t *testing.T) {
	box := newSandbox(t, sandboxConfig)

	if _, err := run(t, "plugin", "enable", "echo", "--config", box.configPath); err != nil {
		t.Fatalf("plugin enable: %v", err)
	}
	if _, err := run(t, "plugin", "order", "echo", "42", "--config", box.configPath); err != nil {
		t.Fatalf("plugin order: %v", err)
	}
	if _, err := run(t, "plugin", "disable", "echo", "--config", box.configPath); err != nil {
		t.Fatalf("plugin disable: %v", err)
	}

	cfg, err := config.Load(box.configPath)
	if err != nil {
		t.Fatalf("改写后的配置不合法: %v", err)
	}
	if len(cfg.Plugins.List) != 1 {
		t.Fatalf("期望 1 条登记，实际 %+v", cfg.Plugins.List)
	}
	entry := cfg.Plugins.List[0]
	if entry.Enabled {
		t.Error("disable 之后仍然是启用状态")
	}
	if entry.Order != 42 {
		t.Errorf("顺序应当是 42，实际 %d", entry.Order)
	}
}

func TestPluginEnableUnknownKeyFails(t *testing.T) {
	box := newSandbox(t, sandboxConfig)

	out, err := run(t, "plugin", "enable", "nope", "--config", box.configPath)
	if err == nil {
		t.Fatal("启用不存在的插件应当报错")
	}
	if !strings.Contains(out, "nope") {
		t.Errorf("错误信息里应当提到 key:\n%s", out)
	}
}

func TestPluginOrderRejectsBadNumber(t *testing.T) {
	box := newSandbox(t, sandboxConfig)

	if _, err := run(t, "plugin", "order", "echo", "-1", "--config", box.configPath); err == nil {
		t.Fatal("负数顺序应当报错")
	}
	if _, err := run(t, "plugin", "order", "echo", "abc", "--config", box.configPath); err == nil {
		t.Fatal("非数字顺序应当报错")
	}
}

func TestPluginCheckPasses(t *testing.T) {
	box := newSandbox(t, sandboxConfig)

	out, err := run(t, "plugin", "check", "--config", box.configPath)
	if err != nil {
		t.Fatalf("plugin check: %v\n%s", err, out)
	}
	if !strings.Contains(out, "全部通过检查") {
		t.Errorf("输出里没有通过结论:\n%s", out)
	}
}

func TestPluginCheckReportsBrokenAndUnregistered(t *testing.T) {
	box := newSandbox(t, sandboxConfig)
	writeFile(t, filepath.Join(box.pluginDir, "broken", "plugin.js"), "export const meta = {")

	out, err := run(t, "plugin", "check", "--config", box.configPath)
	if err == nil {
		t.Fatal("存在致命问题时 check 应当失败")
	}
	for _, want := range []string{"致命", "提示", "broken"} {
		if !strings.Contains(out, want) {
			t.Errorf("检查结果里没有 %q:\n%s", want, out)
		}
	}
}

func TestPluginCheckReportsRegisteredButMissing(t *testing.T) {
	box := newSandbox(t, sandboxConfig+"    - key: ghost\n      enabled: true\n      order: 5\n")

	out, err := run(t, "plugin", "check", "--config", box.configPath)
	if err == nil {
		t.Fatal("配置登记了不存在的插件时 check 应当失败")
	}
	if !strings.Contains(out, "ghost") {
		t.Errorf("检查结果里没有提到 ghost:\n%s", out)
	}
}

func TestPluginCheckOnlyGivenKey(t *testing.T) {
	box := newSandbox(t, sandboxConfig)
	writeFile(t, filepath.Join(box.pluginDir, "broken", "plugin.js"), "export const meta = {")

	out, err := run(t, "plugin", "check", "echo", "--config", box.configPath)
	if err != nil {
		t.Fatalf("只检查 echo 时不该受 broken 影响: %v\n%s", err, out)
	}
	if strings.Contains(out, "broken") {
		t.Errorf("只检查 echo，结果里不该有 broken:\n%s", out)
	}
}

func TestPluginReplay(t *testing.T) {
	box := newSandbox(t, sandboxConfig)

	out, err := run(t, "plugin", "replay", "echo", "--config", box.configPath)
	if err != nil {
		t.Fatalf("plugin replay: %v\n%s", err, out)
	}
	if !strings.Contains(out, "写入标记") || !strings.Contains(out, "通过") {
		t.Errorf("回放结果不对:\n%s", out)
	}
}

func TestPluginReplayReportsFailure(t *testing.T) {
	box := newSandbox(t, sandboxConfig)
	writeFile(t, filepath.Join(box.pluginDir, "echo", "fixture.json"), `{
  "cases": [
    {
      "name": "期望值与实际不符",
      "hook": "onRequest",
      "args": [{ "body": {} }],
      "path": "body.tag",
      "expected": "另一个值"
    }
  ]
}`)

	out, err := run(t, "plugin", "replay", "echo", "--config", box.configPath)
	if err == nil {
		t.Fatal("用例失败时 replay 应当报错")
	}
	if !strings.Contains(out, "失败") {
		t.Errorf("回放结果里没有失败结论:\n%s", out)
	}
}

func TestPluginReplayMissingFixture(t *testing.T) {
	box := newSandbox(t, sandboxConfig)
	if err := os.Remove(filepath.Join(box.pluginDir, "echo", "fixture.json")); err != nil {
		t.Fatalf("删除 fixture 失败: %v", err)
	}

	out, err := run(t, "plugin", "replay", "echo", "--config", box.configPath)
	if err == nil {
		t.Fatal("fixture 缺失时 replay 应当报错")
	}
	if !strings.Contains(out, "--fixture") {
		t.Errorf("错误信息里应当提示 --fixture:\n%s", out)
	}
}

func TestPluginShowJSON(t *testing.T) {
	box := newSandbox(t, sandboxConfig)

	out, err := run(t, "plugin", "show", "echo", "--config", box.configPath, "--json")
	if err != nil {
		t.Fatalf("plugin show --json: %v", err)
	}

	var detail pluginDetail
	if err := json.Unmarshal([]byte(out), &detail); err != nil {
		t.Fatalf("输出不是合法 JSON: %v\n%s", err, out)
	}
	if detail.Key != "echo" || detail.Name != "回显" {
		t.Errorf("自述信息不对: %+v", detail.Summary)
	}
	if detail.SourceBytes <= 0 {
		t.Errorf("源码大小应当为正，实际 %d", detail.SourceBytes)
	}
	if tag, _ := detail.Config["tag"].(string); tag != "sandbox" {
		t.Errorf("config 段不对: %v", detail.Config)
	}
}

func TestPluginNewThenCheck(t *testing.T) {
	box := newSandbox(t, sandboxConfig)

	out, err := run(t, "plugin", "new", "html-escape", "--name", "HTML 转义", "--dir", box.pluginDir)
	if err != nil {
		t.Fatalf("plugin new: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(box.pluginDir, "html-escape", "plugin.js")); err != nil {
		t.Fatalf("插件文件没有生成: %v", err)
	}

	checked, err := run(t, "plugin", "check", "html-escape", "--dir", box.pluginDir)
	if err != nil {
		t.Fatalf("新建的插件应当通过检查: %v\n%s", err, checked)
	}

	replayed, err := run(t, "plugin", "replay", "html-escape", "--dir", box.pluginDir)
	if err != nil {
		t.Fatalf("新建的插件应当能回放: %v\n%s", err, replayed)
	}
}

func TestPluginNewRefusesExistingAndBadKey(t *testing.T) {
	box := newSandbox(t, sandboxConfig)

	if _, err := run(t, "plugin", "new", "echo", "--dir", box.pluginDir); err == nil {
		t.Fatal("key 已存在时应当报错")
	}
	if _, err := run(t, "plugin", "new", "../escape", "--dir", box.pluginDir); err == nil {
		t.Fatal("带路径分隔符的 key 应当被拒绝")
	}
	if _, err := run(t, "plugin", "new", "echo", "--dir", box.pluginDir, "--force"); err != nil {
		t.Fatalf("--force 应当允许覆盖: %v", err)
	}
}

func TestPluginHooks(t *testing.T) {
	out, err := run(t, "plugin", "hooks")
	if err != nil {
		t.Fatalf("plugin hooks: %v", err)
	}
	for _, want := range []string{"onRequest", "onEvent", "onStreamEnd", "onResponse", "50ms", "并发"} {
		if !strings.Contains(out, want) {
			t.Errorf("钩子说明里没有 %q:\n%s", want, out)
		}
	}
}

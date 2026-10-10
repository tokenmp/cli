package cmd

import (
	"os"
	"strings"
	"testing"

	"github.com/tokenmp/core/config"
)

// editableConfig 是增删测试的起点，带注释与一条已存在的渠道、一条引用它的规则。
const editableConfig = `# 沙箱配置
listen: 127.0.0.1:9999

log:
  format: text

channels:
  # 主渠道
  - name: example
    dialect: openai
    base_url: https://api.example.com/v1
    auth:
      style: none

models:
  # 主规则
  - match: "gpt-*"
    channel: example
`

// emptyChannelsConfig 没有任何渠道与规则，用来验证空序列上的追加。
const emptyChannelsConfig = `listen: 127.0.0.1:9999

log:
  format: text

channels: []

models: []
`

// readFile 读回配置文件原文。
func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读配置失败: %v", err)
	}
	return string(data)
}

func TestChannelsCreateKeepsCommentsAndDefaultsAuth(t *testing.T) {
	path := configSandbox(t, editableConfig)

	out, err := run(t, "channels", "create",
		"--name", "extra",
		"--dialect", "claude",
		"--base-url", "https://api.anthropic.com",
		"--key-env", "EXTRA_KEY",
		"--config", path,
	)
	if err != nil {
		t.Fatalf("channels create: %v\n%s", err, out)
	}

	// 给了 --key-env 时 --auth-style 缺省是 bearer。
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("改写后的配置不合法: %v", err)
	}
	channel, ok := cfg.ChannelByName("extra")
	if !ok {
		t.Fatal("配置里没有新渠道")
	}
	if channel.Auth.Style != "bearer" || channel.Auth.KeyEnv != "EXTRA_KEY" {
		t.Errorf("鉴权缺省不对: %+v", channel.Auth)
	}

	// 注释保留，空的可选字段不写出来。
	data := readFile(t, path)
	for _, want := range []string{"# 主渠道", "# 主规则", "- name: extra", "key_env: EXTRA_KEY", "style: bearer"} {
		if !strings.Contains(data, want) {
			t.Errorf("改写结果里没有 %q:\n%s", want, data)
		}
	}
	for _, unwanted := range []string{`path: ""`, "max_retries", `upstream: ""`} {
		if strings.Contains(data, unwanted) {
			t.Errorf("不该写出空的可选字段 %q:\n%s", unwanted, data)
		}
	}
}

func TestChannelsCreateDefaultsAuthToNone(t *testing.T) {
	path := configSandbox(t, emptyChannelsConfig)

	if _, err := run(t, "channels", "create",
		"--name", "local",
		"--dialect", "openai",
		"--base-url", "http://127.0.0.1:8080",
		"--config", path,
	); err != nil {
		t.Fatalf("channels create: %v", err)
	}

	data := readFile(t, path)
	if !strings.Contains(data, "style: none") {
		t.Errorf("没给 --key-env 时鉴权应当是 none:\n%s", data)
	}
	if strings.Contains(data, "key_env") {
		t.Errorf("不鉴权时不该写出 key_env:\n%s", data)
	}
}

func TestChannelsCreateRejectsDuplicate(t *testing.T) {
	path := configSandbox(t, editableConfig)
	before := readFile(t, path)

	out, err := run(t, "channels", "create",
		"--name", "example",
		"--dialect", "openai",
		"--base-url", "https://api.example.com",
		"--config", path,
	)
	if err == nil {
		t.Fatal("重名应当报错")
	}
	if !strings.Contains(out, "已存在") {
		t.Errorf("错误信息里应当说明重名:\n%s", out)
	}
	if readFile(t, path) != before {
		t.Error("重名失败后文件不该改动")
	}
}

func TestChannelsCreateRejectsBaseURLPathWithoutPath(t *testing.T) {
	path := configSandbox(t, emptyChannelsConfig)
	before := readFile(t, path)

	out, err := run(t, "channels", "create",
		"--name", "openrouter",
		"--dialect", "openai",
		"--base-url", "https://openrouter.ai/api/v1",
		"--config", path,
	)
	if err == nil {
		t.Fatal("基址带路径却没给 path 时应当报错")
	}
	if !strings.Contains(out, "path") {
		t.Errorf("错误信息里应当点出 path:\n%s", out)
	}
	if readFile(t, path) != before {
		t.Error("非法取值后文件不该改动")
	}
}

func TestChannelsCreateRejectsBadAuth(t *testing.T) {
	path := configSandbox(t, emptyChannelsConfig)
	before := readFile(t, path)

	out, err := run(t, "channels", "create",
		"--name", "custom",
		"--dialect", "openai",
		"--base-url", "https://api.example.com",
		"--auth-style", "custom",
		"--config", path,
	)
	if err == nil {
		t.Fatal("custom 没给 header 时应当报错")
	}
	if !strings.Contains(out, "header") {
		t.Errorf("错误信息里应当点出 header:\n%s", out)
	}
	if readFile(t, path) != before {
		t.Error("非法取值后文件不该改动")
	}
}

func TestChannelsCreateDryRunDoesNotWrite(t *testing.T) {
	path := configSandbox(t, emptyChannelsConfig)
	before := readFile(t, path)

	out, err := run(t, "channels", "create",
		"--name", "local",
		"--dialect", "openai",
		"--base-url", "http://127.0.0.1:8080",
		"--config", path,
		"--dry-run",
	)
	if err != nil {
		t.Fatalf("channels create --dry-run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "将要追加") || !strings.Contains(out, "local") {
		t.Errorf("dry-run 输出里应当有将要写入的条目:\n%s", out)
	}
	if readFile(t, path) != before {
		t.Error("--dry-run 不该落盘")
	}
}

func TestChannelsDeleteRefusesReferenced(t *testing.T) {
	path := configSandbox(t, editableConfig)
	before := readFile(t, path)

	out, err := run(t, "channels", "delete", "example", "--config", path)
	if err == nil {
		t.Fatal("删除被引用的渠道应当报错")
	}
	for _, want := range []string{"仍被", "models[0]", "gpt-*"} {
		if !strings.Contains(out, want) {
			t.Errorf("错误信息里应当包含 %q:\n%s", want, out)
		}
	}
	if readFile(t, path) != before {
		t.Error("删除失败后文件不该改动")
	}
}

func TestChannelsDeleteAndRemoveAlias(t *testing.T) {
	for _, verb := range []string{"delete", "remove"} {
		t.Run(verb, func(t *testing.T) {
			path := configSandbox(t, emptyChannelsConfig)
			if _, err := run(t, "channels", "create",
				"--name", "local",
				"--dialect", "openai",
				"--base-url", "http://127.0.0.1:8080",
				"--config", path,
			); err != nil {
				t.Fatalf("channels create: %v", err)
			}

			if _, err := run(t, "channels", verb, "local", "--config", path); err != nil {
				t.Fatalf("channels %s: %v", verb, err)
			}

			cfg, err := config.Load(path)
			if err != nil {
				t.Fatalf("删除后配置不合法: %v", err)
			}
			if _, ok := cfg.ChannelByName("local"); ok {
				t.Error("渠道应当已经删掉")
			}
			if !strings.Contains(readFile(t, path), "channels: []") {
				t.Errorf("删到最后一条应当写成 channels: []:\n%s", readFile(t, path))
			}
		})
	}
}

func TestChannelsDeleteMissing(t *testing.T) {
	path := configSandbox(t, editableConfig)

	out, err := run(t, "channels", "delete", "absent", "--config", path)
	if err == nil {
		t.Fatal("删除不存在的渠道应当报错")
	}
	if !strings.Contains(out, "没有渠道") {
		t.Errorf("错误信息里应当说明渠道不存在:\n%s", out)
	}
}

func TestChannelsDeleteDryRunDoesNotWrite(t *testing.T) {
	path := configSandbox(t, emptyChannelsConfig)
	if _, err := run(t, "channels", "create",
		"--name", "local",
		"--dialect", "openai",
		"--base-url", "http://127.0.0.1:8080",
		"--config", path,
	); err != nil {
		t.Fatalf("channels create: %v", err)
	}
	before := readFile(t, path)

	out, err := run(t, "channels", "delete", "local", "--config", path, "--dry-run")
	if err != nil {
		t.Fatalf("channels delete --dry-run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "将要删除") {
		t.Errorf("dry-run 输出里应当说明将要删除:\n%s", out)
	}
	if readFile(t, path) != before {
		t.Error("--dry-run 不该落盘")
	}
}

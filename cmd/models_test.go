package cmd

import (
	"net/http"
	"strings"
	"testing"

	"github.com/tokenmp/core/config"
)

func TestModelsCreateAndOmitsEmptyOptionalFields(t *testing.T) {
	path := configSandbox(t, editableConfig)

	out, err := run(t, "models", "create",
		"--match", "claude-*",
		"--channel", "example",
		"--config", path,
	)
	if err != nil {
		t.Fatalf("models create: %v\n%s", err, out)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("改写后的配置不合法: %v", err)
	}
	if len(cfg.Models) != 2 {
		t.Fatalf("规则数量不对: %+v", cfg.Models)
	}
	added := cfg.Models[1]
	if added.Match != "claude-*" || added.Channel != "example" {
		t.Errorf("新规则不对: %+v", added)
	}
	if added.Upstream != "" || added.Dialect != "" || added.Affinity != "" {
		t.Errorf("没给的可选字段不该有值: %+v", added)
	}
	// 空的可选字段不写出来。
	if data := readFile(t, path); strings.Contains(data, "upstream") || strings.Contains(data, "affinity") {
		t.Errorf("不该写出空的可选字段:\n%s", data)
	}
	// 注释保留。
	if data := readFile(t, path); !strings.Contains(data, "# 主规则") {
		t.Errorf("注释丢了:\n%s", data)
	}
}

func TestModelsCreateUnknownChannel(t *testing.T) {
	path := configSandbox(t, editableConfig)
	before := readFile(t, path)

	out, err := run(t, "models", "create",
		"--match", "claude-*",
		"--channel", "ghost",
		"--config", path,
	)
	if err == nil {
		t.Fatal("引用不存在的渠道应当报错")
	}
	if !strings.Contains(out, "ghost") {
		t.Errorf("错误信息里应当点出渠道名:\n%s", out)
	}
	if readFile(t, path) != before {
		t.Error("失败后文件不该改动")
	}
}

func TestModelsCreateRejectsDuplicate(t *testing.T) {
	path := configSandbox(t, editableConfig)
	before := readFile(t, path)

	out, err := run(t, "models", "create",
		"--match", "gpt-*",
		"--channel", "example",
		"--config", path,
	)
	if err == nil {
		t.Fatal("重名规则应当报错")
	}
	if !strings.Contains(out, "已存在") {
		t.Errorf("错误信息里应当说明重复:\n%s", out)
	}
	if readFile(t, path) != before {
		t.Error("重名失败后文件不该改动")
	}
}

func TestModelsCreateRollsBackInvalidUpstream(t *testing.T) {
	path := configSandbox(t, editableConfig)
	before := readFile(t, path)

	// ${bad} 能通过命令层的前置检查，写回后由 config.Load 拒绝，configedit 负责还原。
	out, err := run(t, "models", "create",
		"--match", "claude-*",
		"--channel", "example",
		"--upstream", "x/${bad}",
		"--config", path,
	)
	if err == nil {
		t.Fatal("非法占位符应当报错")
	}
	if !strings.Contains(out, "已还原") {
		t.Errorf("错误信息里应当说明已还原:\n%s", out)
	}
	if readFile(t, path) != before {
		t.Error("校验失败后文件应当还原")
	}
}

func TestModelsCreateDryRunDoesNotWrite(t *testing.T) {
	path := configSandbox(t, editableConfig)
	before := readFile(t, path)

	out, err := run(t, "models", "create",
		"--match", "claude-*",
		"--channel", "example",
		"--config", path,
		"--dry-run",
	)
	if err != nil {
		t.Fatalf("models create --dry-run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "将要追加") || !strings.Contains(out, "claude-*") {
		t.Errorf("dry-run 输出里应当有将要写入的条目:\n%s", out)
	}
	if readFile(t, path) != before {
		t.Error("--dry-run 不该落盘")
	}
}

func TestModelsDeleteByIndex(t *testing.T) {
	path := configSandbox(t, editableConfig)

	out, err := run(t, "models", "delete", "0", "--config", path)
	if err != nil {
		t.Fatalf("models delete 0: %v\n%s", err, out)
	}
	if !strings.Contains(out, "gpt-*") || !strings.Contains(out, "example") {
		t.Errorf("删除结果里应当说明删掉的是哪一条:\n%s", out)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("删除后配置不合法: %v", err)
	}
	if len(cfg.Models) != 0 {
		t.Errorf("规则应当已经删掉: %+v", cfg.Models)
	}
	if !strings.Contains(readFile(t, path), "models: []") {
		t.Errorf("删到一条不剩应当写成 models: []:\n%s", readFile(t, path))
	}
}

func TestModelsDeleteByMatchAndChannel(t *testing.T) {
	path := configSandbox(t, affinityConfig)

	out, err := run(t, "models", "delete", "deepseek-*", "--channel", "account-b", "--config", path)
	if err != nil {
		t.Fatalf("models delete: %v\n%s", err, out)
	}
	if !strings.Contains(out, "account-b") {
		t.Errorf("删除结果里应当说明渠道:\n%s", out)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("删除后配置不合法: %v", err)
	}
	if len(cfg.Models) != 1 || cfg.Models[0].Channel != "account-a" {
		t.Errorf("应当只剩下 account-a 那条: %+v", cfg.Models)
	}
}

func TestModelsDeleteAmbiguousMatch(t *testing.T) {
	path := configSandbox(t, affinityConfig)

	out, err := run(t, "models", "delete", "deepseek-*", "--config", path)
	if err == nil {
		t.Fatal("匹配式对应多条规则时应当报错")
	}
	for _, want := range []string{"models.0", "models.1", "account-a", "account-b", "--channel"} {
		if !strings.Contains(out, want) {
			t.Errorf("错误信息里应当包含 %q:\n%s", want, out)
		}
	}
}

func TestModelsDeleteErrors(t *testing.T) {
	path := configSandbox(t, editableConfig)

	if out, err := run(t, "models", "delete", "9", "--config", path); err == nil {
		t.Error("序号越界应当报错")
	} else if !strings.Contains(out, "越界") {
		t.Errorf("错误信息里应当说明越界:\n%s", out)
	}

	if out, err := run(t, "models", "delete", "nope-*", "--config", path); err == nil {
		t.Error("匹配不到规则应当报错")
	} else if !strings.Contains(out, "没有") {
		t.Errorf("错误信息里应当说明没有匹配:\n%s", out)
	}

	if out, err := run(t, "models", "delete", "0", "--channel", "ghost", "--config", path); err == nil {
		t.Error("序号与渠道不符时应当报错")
	} else if !strings.Contains(out, "不符") {
		t.Errorf("错误信息里应当说明不符:\n%s", out)
	}
}

func TestModelsDeleteDryRunDoesNotWrite(t *testing.T) {
	path := configSandbox(t, editableConfig)
	before := readFile(t, path)

	out, err := run(t, "models", "delete", "0", "--config", path, "--dry-run")
	if err != nil {
		t.Fatalf("models delete --dry-run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "将要删除") || !strings.Contains(out, "models.0") {
		t.Errorf("dry-run 输出里应当说明将要删除哪一条:\n%s", out)
	}
	if readFile(t, path) != before {
		t.Error("--dry-run 不该落盘")
	}
}

// TestChannelsModelsBootstrapFlow 从空配置走一遍完整引导：建渠道、建规则、校验、
// 列表、试算，最后用 tokenmp test 打一次假上游。
func TestChannelsModelsBootstrapFlow(t *testing.T) {
	upstream, received := newTestUpstream(t, http.StatusOK, "application/json", openAIChatUpstream)
	path := configSandbox(t, emptyChannelsConfig)

	if out, err := run(t, "channels", "create",
		"--name", "fake",
		"--dialect", "openai",
		"--base-url", upstream.URL,
		"--auth-style", "none",
		"--config", path,
	); err != nil {
		t.Fatalf("channels create: %v\n%s", err, out)
	}

	if out, err := run(t, "models", "create",
		"--match", "test-model",
		"--channel", "fake",
		"--config", path,
	); err != nil {
		t.Fatalf("models create: %v\n%s", err, out)
	}

	if out, err := run(t, "config", "validate", "--config", path); err != nil {
		t.Fatalf("config validate: %v\n%s", err, out)
	}

	listed, err := run(t, "channels", "list", "--config", path)
	if err != nil {
		t.Fatalf("channels list: %v\n%s", err, listed)
	}
	if !strings.Contains(listed, "fake") {
		t.Errorf("渠道表里没有 fake:\n%s", listed)
	}

	resolved, err := run(t, "models", "list", "--config", path, "--model", "test-model")
	if err != nil {
		t.Fatalf("models list --model: %v\n%s", err, resolved)
	}
	for _, want := range []string{"候选路由", "fake", "test-model"} {
		if !strings.Contains(resolved, want) {
			t.Errorf("试算结果里没有 %q:\n%s", want, resolved)
		}
	}

	tested, err := run(t, "test", "--config", path, "--model", "test-model", "--prompt", "你好")
	if err != nil {
		t.Fatalf("test: %v\n%s", err, tested)
	}
	for _, want := range []string{"请求结果", "来自假上游", "fake"} {
		if !strings.Contains(tested, want) {
			t.Errorf("测试结果里没有 %q:\n%s", want, tested)
		}
	}
	if received.String() == "" {
		t.Error("假上游没有收到请求")
	}
}

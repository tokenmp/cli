package cmd

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/tokenmp/core/config"
	"github.com/tokenmp/core/dialect"
)

// importSampleJSON 是一份常见的渠道列表响应：四个渠道、五种模型，覆盖三个方言。
const importSampleJSON = `{
  "success": true,
  "message": "",
  "data": {
    "items": [
      {
        "id": 1,
        "name": "openai",
        "type": 1,
        "key": "",
        "base_url": "https://api.openai.com",
        "models": "gpt-4o,gpt-4o-mini"
      },
      {
        "id": 2,
        "name": "openrouter",
        "type": 20,
        "key": "",
        "base_url": "https://openrouter.ai/api",
        "models": "anthropic/claude-sonnet-4"
      },
      {
        "id": 3,
        "name": "anthropic",
        "type": 14,
        "key": "",
        "base_url": "https://api.anthropic.com",
        "models": "claude-sonnet-4"
      },
      {
        "id": 4,
        "name": "gemini",
        "type": 24,
        "key": "",
        "base_url": "https://generativelanguage.googleapis.com",
        "models": "gemini-2.0-flash"
      }
    ],
    "total": 4,
    "page": 1,
    "page_size": 100
  }
}`

// importFile 把一段 JSON 写进临时文件并返回路径。
func importFile(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "channels.json")
	writeFile(t, path, content)
	return path
}

func TestImportNewAPIAppendsChannelsAndRules(t *testing.T) {
	path := configSandbox(t, editableConfig)
	file := importFile(t, importSampleJSON)

	out, err := run(t, "import", "--from", "new-api", "--file", file, "--config", path)
	if err != nil {
		t.Fatalf("import: %v\n%s", err, out)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("导入后的配置不合法: %v", err)
	}
	if len(cfg.Channels) != 5 {
		t.Fatalf("渠道数不对，想要 5 条（含原有的 example），实际 %d 条", len(cfg.Channels))
	}
	if len(cfg.Models) != 6 {
		t.Fatalf("规则数不对，想要 6 条（含原有的 gpt-*），实际 %d 条", len(cfg.Models))
	}

	cases := []struct {
		name    string
		dialect string
		baseURL string
		path    string
		style   string
		keyEnv  string
		header  string
	}{
		{"openai", "openai", "https://api.openai.com", "", "bearer", "OPENAI_API_KEY", ""},
		{"openrouter", "openai", "https://openrouter.ai/api", "/v1/chat/completions", "bearer", "OPENROUTER_API_KEY", ""},
		{"anthropic", "claude", "https://api.anthropic.com", "", "x-api-key", "ANTHROPIC_API_KEY", ""},
		{"gemini", "gemini", "https://generativelanguage.googleapis.com", "", "custom", "GEMINI_API_KEY", "x-goog-api-key"},
	}
	for _, want := range cases {
		channel, ok := cfg.ChannelByName(want.name)
		if !ok {
			t.Errorf("配置里没有渠道 %s", want.name)
			continue
		}
		if channel.Dialect != want.dialect || channel.BaseURL != want.baseURL || channel.Path != want.path {
			t.Errorf("渠道 %s 不对: %+v", want.name, channel)
		}
		if channel.Auth.Style != want.style || channel.Auth.KeyEnv != want.keyEnv || channel.Auth.Header != want.header {
			t.Errorf("渠道 %s 的鉴权不对: %+v", want.name, channel.Auth)
		}
	}

	rules := map[string]string{}
	for _, rule := range cfg.Models {
		rules[rule.Match] = rule.Channel
	}
	for model, channel := range map[string]string{
		"gpt-4o":                    "openai",
		"gpt-4o-mini":               "openai",
		"anthropic/claude-sonnet-4": "openrouter",
		"claude-sonnet-4":           "anthropic",
		"gemini-2.0-flash":          "gemini",
	} {
		if rules[model] != channel {
			t.Errorf("规则 %s 应当指向 %s，实际 %q", model, channel, rules[model])
		}
	}

	// 原有注释要留着。
	data := readFile(t, path)
	for _, want := range []string{"# 主渠道", "# 主规则", "key_env: ANTHROPIC_API_KEY", "header: x-goog-api-key"} {
		if !strings.Contains(data, want) {
			t.Errorf("导入结果里没有 %q:\n%s", want, data)
		}
	}
	// 密钥值绝不写进配置。
	if strings.Contains(data, "key: ") {
		t.Errorf("配置里不该出现密钥字段:\n%s", data)
	}

	for _, want := range []string{"OPENAI_API_KEY", "OPENROUTER_API_KEY", "ANTHROPIC_API_KEY", "GEMINI_API_KEY", "渠道"} {
		if !strings.Contains(out, want) {
			t.Errorf("输出里没有 %q:\n%s", want, out)
		}
	}

	// 导入后配置能过校验，也能被两个 list 命令读出来。
	if _, err := run(t, "config", "validate", "--config", path); err != nil {
		t.Errorf("config validate: %v", err)
	}
	listed, err := run(t, "channels", "list", "--config", path)
	if err != nil {
		t.Fatalf("channels list: %v", err)
	}
	for _, want := range []string{"openrouter", "/v1/chat/completions", "anthropic", "gemini"} {
		if !strings.Contains(listed, want) {
			t.Errorf("channels list 里没有 %q:\n%s", want, listed)
		}
	}
}

func TestImportNewAPIDryRunDoesNotWrite(t *testing.T) {
	path := configSandbox(t, editableConfig)
	before := readFile(t, path)
	file := importFile(t, importSampleJSON)

	out, err := run(t, "import", "--from", "new-api", "--file", file, "--config", path, "--dry-run")
	if err != nil {
		t.Fatalf("import --dry-run: %v\n%s", err, out)
	}
	if after := readFile(t, path); after != before {
		t.Errorf("--dry-run 不该改配置文件:\n之前:\n%s\n之后:\n%s", before, after)
	}
	if _, err := config.Load(path); err != nil {
		t.Fatalf("--dry-run 后配置应当不变且合法: %v", err)
	}

	for _, want := range []string{"4 条", "5 条", "OPENAI_API_KEY", "没有写进配置文件"} {
		if !strings.Contains(out, want) {
			t.Errorf("--dry-run 输出里没有 %q:\n%s", want, out)
		}
	}
}

func TestImportNewAPIRejectsUnsupportedType(t *testing.T) {
	path := configSandbox(t, editableConfig)
	before := readFile(t, path)
	file := importFile(t, `{"data":{"items":[
		{"id": 7, "name": "mj", "type": 2, "base_url": "https://mj.example.com", "models": "midjourney"},
		{"id": 8, "name": "ghost", "type": 999, "base_url": "https://ghost.example.com", "models": "m"}
	]}}`)

	out, err := run(t, "import", "--from", "new-api", "--file", file, "--config", path)
	if err == nil {
		t.Fatalf("不在映射表里的类型应当报错\n%s", out)
	}
	for _, want := range []string{"mj", "2", "Midjourney", "ghost", "999"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("报错里没有 %q: %v", want, err)
		}
	}
	if after := readFile(t, path); after != before {
		t.Errorf("报错时不该改配置文件:\n%s", after)
	}
}

func TestImportNewAPISkipsDuplicateChannels(t *testing.T) {
	path := configSandbox(t, editableConfig)
	file := importFile(t, importSampleJSON)

	if _, err := run(t, "import", "--from", "new-api", "--file", file, "--config", path); err != nil {
		t.Fatalf("第一次 import: %v", err)
	}
	first := readFile(t, path)

	out, err := run(t, "import", "--from", "new-api", "--file", file, "--config", path)
	if err != nil {
		t.Fatalf("第二次 import: %v\n%s", err, out)
	}
	if after := readFile(t, path); after != first {
		t.Errorf("重复导入不该写出重复条目:\n%s", after)
	}
	if !strings.Contains(out, "配置里已有同名渠道") {
		t.Errorf("输出里应当说明渠道被跳过:\n%s", out)
	}
	if !strings.Contains(out, "渠道: 0 条") || !strings.Contains(out, "规则: 0 条") {
		t.Errorf("重复导入不该新增条目:\n%s", out)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("配置不合法: %v", err)
	}
	if len(cfg.Channels) != 5 || len(cfg.Models) != 6 {
		t.Errorf("重复导入后条目数变了: 渠道 %d、规则 %d", len(cfg.Channels), len(cfg.Models))
	}
}

func TestImportNewAPISkipsDuplicateInsideFile(t *testing.T) {
	path := configSandbox(t, emptyChannelsConfig)
	file := importFile(t, `{"data":{"items":[
		{"id": 1, "name": "openai", "type": 1, "base_url": "https://api.openai.com", "models": "gpt-4o"},
		{"id": 2, "name": "openai", "type": 1, "base_url": "https://other.example.com", "models": "gpt-4o-mini"}
	]}}`)

	out, err := run(t, "import", "--from", "new-api", "--file", file, "--config", path)
	if err != nil {
		t.Fatalf("import: %v\n%s", err, out)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("配置不合法: %v", err)
	}
	if len(cfg.Channels) != 1 {
		t.Errorf("文件内重名应当只留一条渠道，实际 %d 条", len(cfg.Channels))
	}
	if !strings.Contains(out, "同一份文件里重名") {
		t.Errorf("输出里应当说明文件内重名:\n%s", out)
	}
}

func TestImportNewAPIDerivesPathFromBaseURL(t *testing.T) {
	path := configSandbox(t, emptyChannelsConfig)
	file := importFile(t, `{"data":{"items":[
		{"id": 1, "name": "with-v1", "type": 1, "base_url": "https://api.example.com/v1", "models": ""},
		{"id": 2, "name": "with-prefix", "type": 1, "base_url": "https://api.example.com/api/v1", "models": ""},
		{"id": 3, "name": "plain", "type": 1, "base_url": "https://api.example.com", "models": ""},
		{"id": 4, "name": "covered", "type": 1, "base_url": "https://api.example.com/v1/chat/completions", "models": ""},
		{"id": 5, "name": "gemini-path", "type": 24, "base_url": "https://generativelanguage.googleapis.com/v1beta", "models": ""}
	]}}`)

	out, err := run(t, "import", "--from", "new-api", "--file", file, "--config", path)
	if err != nil {
		t.Fatalf("import: %v\n%s", err, out)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("配置不合法: %v", err)
	}

	paths := map[string]string{}
	for _, channel := range cfg.Channels {
		paths[channel.Name] = channel.Path
	}
	want := map[string]string{
		"with-v1":     "/chat/completions",
		"with-prefix": "/chat/completions",
		"plain":       "",
	}
	for name, path := range want {
		if paths[name] != path {
			t.Errorf("渠道 %s 的 path 想要 %q，实际 %q", name, path, paths[name])
		}
	}
	if _, ok := paths["covered"]; ok {
		t.Errorf("base_url 已含完整路径时应当跳过")
	}
	if _, ok := paths["gemini-path"]; ok {
		t.Errorf("gemini 带路径的 base_url 应当跳过")
	}
	for _, wantReason := range []string{"covered", "gemini-path", "拆不出", "模型名"} {
		if !strings.Contains(out, wantReason) {
			t.Errorf("输出里没有 %q:\n%s", wantReason, out)
		}
	}
}

func TestImportNewAPISkipsMissingBaseURL(t *testing.T) {
	path := configSandbox(t, emptyChannelsConfig)
	file := importFile(t, `{"data":{"items":[
		{"id": 1, "name": "nobase", "type": 1, "base_url": "", "models": "gpt-4o"}
	]}}`)

	out, err := run(t, "import", "--from", "new-api", "--file", file, "--config", path)
	if err != nil {
		t.Fatalf("import: %v\n%s", err, out)
	}
	if !strings.Contains(out, "没有 base_url") {
		t.Errorf("输出里应当说明缺少 base_url:\n%s", out)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("配置不合法: %v", err)
	}
	if len(cfg.Channels) != 0 || len(cfg.Models) != 0 {
		t.Errorf("没有 base_url 的渠道不该写进配置: 渠道 %d、规则 %d", len(cfg.Channels), len(cfg.Models))
	}
}

func TestImportNewAPIKeepsKeyOutOfConfig(t *testing.T) {
	path := configSandbox(t, emptyChannelsConfig)
	file := importFile(t, `{"data":{"items":[
		{"id": 1, "name": "openai", "type": 1, "key": "sk-should-not-be-written", "base_url": "https://api.openai.com", "models": ""}
	]}}`)

	out, err := run(t, "import", "--from", "new-api", "--file", file, "--config", path)
	if err != nil {
		t.Fatalf("import: %v\n%s", err, out)
	}
	data := readFile(t, path)
	if strings.Contains(data, "sk-should-not-be-written") {
		t.Errorf("密钥不该写进配置:\n%s", data)
	}
	if !strings.Contains(data, "OPENAI_API_KEY") {
		t.Errorf("应当只写环境变量名:\n%s", data)
	}
	if !strings.Contains(out, "带了 key") {
		t.Errorf("输出里应当提醒 JSON 里带了 key:\n%s", out)
	}
}

func TestImportNewAPIRenamesCollidingKeyEnv(t *testing.T) {
	path := configSandbox(t, emptyChannelsConfig)
	file := importFile(t, `{"data":{"items":[
		{"id": 1, "name": "foo-bar", "type": 1, "base_url": "https://a.example.com", "models": ""},
		{"id": 2, "name": "foo_bar", "type": 1, "base_url": "https://b.example.com", "models": ""}
	]}}`)

	out, err := run(t, "import", "--from", "new-api", "--file", file, "--config", path)
	if err != nil {
		t.Fatalf("import: %v\n%s", err, out)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("配置不合法: %v", err)
	}
	first, _ := cfg.ChannelByName("foo-bar")
	second, _ := cfg.ChannelByName("foo_bar")
	if first.Auth.KeyEnv != "FOO_BAR_API_KEY" {
		t.Errorf("第一条凭据变量不对: %s", first.Auth.KeyEnv)
	}
	if second.Auth.KeyEnv != "FOO_BAR_API_KEY_2" {
		t.Errorf("冲突时应当换名，实际 %s", second.Auth.KeyEnv)
	}
	if !strings.Contains(out, "已被占用") {
		t.Errorf("输出里应当说明换名:\n%s", out)
	}
}

func TestImportNewAPIRejectsBadFlags(t *testing.T) {
	path := configSandbox(t, emptyChannelsConfig)

	if _, err := run(t, "import", "--file", "channels.json", "--config", path); err == nil {
		t.Error("缺少 --from 时应当报错")
	}
	if _, err := run(t, "import", "--from", "one-api", "--file", "channels.json", "--config", path); err == nil {
		t.Error("不支持的数据源应当报错")
	}
	if _, err := run(t, "import", "--from", "new-api", "--config", path); err == nil {
		t.Error("缺少 --file 时应当报错")
	}
	if _, err := run(t, "import", "--from", "new-api", "--file", filepath.Join(t.TempDir(), "nope.json"), "--config", path); err == nil {
		t.Error("文件不存在时应当报错")
	}
}

func TestParseNewAPIChannelsShapes(t *testing.T) {
	for name, content := range map[string]string{
		"完整信封":     `{"success": true, "data": {"items": [{"name": "a"}]}}`,
		"只含 items": `{"items": [{"name": "a"}]}`,
		"纯数组":      `[{"name": "a"}]`,
	} {
		t.Run(name, func(t *testing.T) {
			channels, err := parseNewAPIChannels([]byte(content))
			if err != nil {
				t.Fatalf("解析失败: %v", err)
			}
			if len(channels) != 1 || channels[0].Name != "a" {
				t.Errorf("解析结果不对: %+v", channels)
			}
		})
	}
}

func TestParseNewAPIChannelsErrors(t *testing.T) {
	if _, err := parseNewAPIChannels([]byte("")); err == nil {
		t.Error("空输入应当报错")
	}
	if _, err := parseNewAPIChannels([]byte("{}")); err == nil {
		t.Error("没有 items 时应当报错")
	}
	if _, err := parseNewAPIChannels([]byte(`{"success": false, "message": "没有权限"}`)); err == nil {
		t.Error("响应不成功时应当报错")
	}
}

func TestNormalizeEnvName(t *testing.T) {
	cases := map[string]string{
		"openai":       "OPENAI",
		"command-code": "COMMAND_CODE",
		"foo__bar":     "FOO_BAR",
		"-edge-":       "EDGE",
		"v2.gpt":       "V2_GPT",
		"中文":           "CHANNEL",
	}
	for input, want := range cases {
		if got := normalizeEnvName(input); got != want {
			t.Errorf("normalizeEnvName(%q) = %q，想要 %q", input, got, want)
		}
	}
}

func TestNewAPIChannelTypesTable(t *testing.T) {
	// 编号不能重复，否则查表会先命中后一条。
	seen := map[int]bool{}
	for _, item := range newAPIChannelTypes {
		if seen[item.id] {
			t.Errorf("类型编号 %d 重复", item.id)
		}
		seen[item.id] = true
		if item.name == "" {
			t.Errorf("类型 %d 没有名字", item.id)
		}
	}

	want := map[int]dialect.Name{
		1:  dialect.OpenAIChat,
		14: dialect.ClaudeMessages,
		20: dialect.OpenAIChat,
		24: dialect.GeminiGenerate,
		41: dialect.GeminiGenerate,
		43: dialect.OpenAIChat,
		53: dialect.OpenAIChat,
	}
	for id, name := range want {
		item, ok := newAPIChannelTypeByID(id)
		if !ok {
			t.Fatalf("类型 %d 不在映射表里", id)
		}
		if item.dialect != name {
			t.Errorf("类型 %d 想要 %s，实际 %q", id, name, item.dialect)
		}
	}

	// 表外的类型必须留空，不能默认归到 openai。
	for _, id := range []int{0, 2, 3, 8, 27, 45, 57} {
		item, ok := newAPIChannelTypeByID(id)
		if !ok {
			t.Fatalf("类型 %d 不在表里", id)
		}
		if item.dialect != "" {
			t.Errorf("类型 %d 不该映射到 %q", id, item.dialect)
		}
	}
}

func TestImportChannelPath(t *testing.T) {
	cases := []struct {
		dialect dialect.Name
		baseURL string
		want    string
		wantErr bool
	}{
		{dialect.OpenAIChat, "https://api.openai.com", "", false},
		{dialect.OpenAIChat, "https://openrouter.ai/api", "/v1/chat/completions", false},
		{dialect.OpenAIChat, "https://api.example.com/v1", "/chat/completions", false},
		{dialect.OpenAIChat, "https://api.example.com/api/v1", "/chat/completions", false},
		{dialect.ClaudeMessages, "https://api.anthropic.com/v1", "/messages", false},
		{dialect.OpenAIChat, "https://api.example.com/v1/chat/completions", "", true},
		{dialect.GeminiGenerate, "https://generativelanguage.googleapis.com/v1beta", "", true},
		{dialect.GeminiGenerate, "https://generativelanguage.googleapis.com", "", false},
	}
	for _, want := range cases {
		path, err := importChannelPath(want.dialect, want.baseURL)
		if want.wantErr {
			if err == nil {
				t.Errorf("%s %s 应当报错，实际得到 %q", want.dialect, want.baseURL, path)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s %s 不该报错: %v", want.dialect, want.baseURL, err)
			continue
		}
		if path != want.want {
			t.Errorf("%s %s 的 path = %q，想要 %q", want.dialect, want.baseURL, path, want.want)
		}
	}
}

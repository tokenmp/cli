package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/tokenmp/core/config"

	"github.com/tokenmp/cli/internal/configmgr"
)

// accountsConfig 是带顶层账号的沙箱配置：渠道 pool 引用 team-a 与 team-b，
// 另有 plain-a、plain-b 两条直接给 key_env 的渠道，规则指向 pool。
//
// 账号的 key_env 特意写成 _API_KEY 结尾，导入测试可以用同名渠道验证生成的
// 环境变量名不会与账号撞车。
const accountsConfig = `# 顶层账号沙箱配置
listen: 127.0.0.1:9999

accounts:
  - {name: team-a, key_env: TEAM_A_API_KEY}
  - {name: team-b, key_env: TEAM_B_API_KEY}

channels:
  - name: pool
    dialect: openai
    base_url: "https://b.example.com/v1"
    path: /chat/completions
    auth: {style: bearer, accounts: [team-a, team-b]}
  - name: plain-a
    dialect: openai
    base_url: "https://a.example.com/v1"
    path: /chat/completions
    auth: {style: bearer, key_env: PLAIN_A_API_KEY}
  - name: plain-b
    dialect: openai
    base_url: "https://c.example.com/v1"
    path: /chat/completions
    auth: {style: bearer, key_env: PLAIN_B_API_KEY}

models:
  - {match: "m-*", channel: pool}
`

// TestChannelsDeleteUsesFilePositions 是 issue #25 的核心回归：账号展开之后第二条
// 变成了 plain-b，删除 plain-a 必须按文件里的下标删掉 plain-a 本身。
func TestChannelsDeleteUsesFilePositions(t *testing.T) {
	path := configSandbox(t, accountsConfig)

	out, err := run(t, "channels", "delete", "plain-a", "--config", path)
	if err != nil {
		t.Fatalf("channels delete plain-a: %v\n%s", err, out)
	}
	if !strings.Contains(out, "channels.1") {
		t.Errorf("删除结果应当报文件里的下标 channels.1:\n%s", out)
	}

	data := readFile(t, path)
	if strings.Contains(data, "name: plain-a") {
		t.Errorf("plain-a 应当被删掉:\n%s", data)
	}
	if !strings.Contains(data, "name: plain-b") {
		t.Errorf("plain-b 必须原样保留:\n%s", data)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("删除后配置不合法: %v", err)
	}
	if _, ok := cfg.ChannelByName("plain-a"); ok {
		t.Error("展开结果里不该还有 plain-a")
	}
	if _, ok := cfg.ChannelByName("plain-b"); !ok {
		t.Error("展开结果里应当还有 plain-b")
	}
}

// TestChannelsListShowsAccountReferences 验证列表按文件里的名字与账号引用显示。
func TestChannelsListShowsAccountReferences(t *testing.T) {
	path := configSandbox(t, accountsConfig)

	out, err := run(t, "channels", "list", "--config", path)
	if err != nil {
		t.Fatalf("channels list: %v\n%s", err, out)
	}
	if !strings.Contains(out, "bearer ← accounts: team-a, team-b") {
		t.Errorf("引用账号的渠道应当在鉴权列写出账号名:\n%s", out)
	}
	for _, want := range []string{"pool", "plain-a", "plain-b"} {
		if !strings.Contains(out, want) {
			t.Errorf("渠道表里没有文件里的名字 %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "pool/team-a") {
		t.Errorf("列表不该出现只在运行时才有的展开名:\n%s", out)
	}
}

// TestChannelsListAccountCredentialStatus 验证凭据状态按引用的账号判断：
// 全部账号的 key_env 都已设置才算已设置，否则未设置。
func TestChannelsListAccountCredentialStatus(t *testing.T) {
	path := configSandbox(t, accountsConfig)

	credentialOf := func(t *testing.T) string {
		t.Helper()
		out, err := run(t, "channels", "list", "--config", path, "--json")
		if err != nil {
			t.Fatalf("channels list --json: %v\n%s", err, out)
		}
		var rows []channelRow
		if err := json.Unmarshal([]byte(out), &rows); err != nil {
			t.Fatalf("输出不是合法 JSON: %v\n%s", err, out)
		}
		for _, row := range rows {
			if row.Name == "pool" {
				if row.Auth != "bearer ← accounts: team-a, team-b" {
					t.Errorf("鉴权一列不对: %q", row.Auth)
				}
				return row.Credential
			}
		}
		t.Fatal("JSON 里没有 pool")
		return ""
	}

	if state := credentialOf(t); state != credentialMissing {
		t.Errorf("两个账号的 key_env 都没设时应当是 %q，实际 %q", credentialMissing, state)
	}

	t.Setenv("TEAM_A_API_KEY", "a")
	if state := credentialOf(t); state != credentialMissing {
		t.Errorf("只设了一个账号时应当是 %q，实际 %q", credentialMissing, state)
	}

	t.Setenv("TEAM_B_API_KEY", "b")
	if state := credentialOf(t); state != credentialPresent {
		t.Errorf("两个账号都设好时应当是 %q，实际 %q", credentialPresent, state)
	}
}

// TestChannelsDeleteRefusesAccountChannelByName 验证按文件里的基础名定位账号渠道，
// 引用检查用的是文件里的 models 下标。
func TestChannelsDeleteRefusesAccountChannelByName(t *testing.T) {
	path := configSandbox(t, accountsConfig)
	before := readFile(t, path)

	out, err := run(t, "channels", "delete", "pool", "--config", path)
	if err == nil {
		t.Fatal("删除被引用的渠道应当报错")
	}
	for _, want := range []string{"仍被", "models[0]", "m-*"} {
		if !strings.Contains(out, want) {
			t.Errorf("拒绝信息里应当包含 %q:\n%s", want, out)
		}
	}
	if readFile(t, path) != before {
		t.Error("被拒绝后文件不该改动")
	}
}

// TestModelsListKeepsFileIndexes 验证规则表与序号按文件里的写法，试算按展开形态。
func TestModelsListKeepsFileIndexes(t *testing.T) {
	path := configSandbox(t, accountsConfig)

	rules, err := run(t, "models", "list", "--config", path)
	if err != nil {
		t.Fatalf("models list: %v\n%s", err, rules)
	}
	if !strings.Contains(rules, "pool") || strings.Contains(rules, "pool/team-a") {
		t.Errorf("规则表应当给文件里的渠道名 pool:\n%s", rules)
	}

	trial, err := run(t, "models", "list", "--config", path, "--model", "m-1", "--json")
	if err != nil {
		t.Fatalf("models list --model: %v\n%s", err, trial)
	}
	var output modelsOutput
	if err := json.Unmarshal([]byte(trial), &output); err != nil {
		t.Fatalf("输出不是合法 JSON: %v\n%s", err, trial)
	}
	if len(output.Rules) != 1 || output.Rules[0].Index != 0 || output.Rules[0].Channel != "pool" {
		t.Fatalf("规则表应当按文件给出序号与渠道名: %+v", output.Rules)
	}
	if len(output.Routes) != 2 {
		t.Fatalf("试算应当给展开之后的 2 条候选: %+v", output.Routes)
	}
	if output.Routes[0].Channel != "pool/team-a" || output.Routes[1].Channel != "pool/team-b" {
		t.Errorf("候选渠道应当是展开名: %+v", output.Routes)
	}
}

// TestModelsDeleteUsesFilePositions 验证 models delete 的下标与 models list 一致。
func TestModelsDeleteUsesFilePositions(t *testing.T) {
	path := configSandbox(t, accountsConfig)

	if out, err := run(t, "models", "delete", "0", "--config", path); err != nil {
		t.Fatalf("models delete 0: %v\n%s", err, out)
	}
	if data := readFile(t, path); strings.Contains(data, "m-*") {
		t.Errorf("m-* 规则应当被删掉:\n%s", data)
	}
	if _, err := config.Load(path); err != nil {
		t.Fatalf("删除后配置不合法: %v", err)
	}
}

// TestConfigKeepsAccounts 验证 config get、show 与 validate 都按文件里的写法取值。
func TestConfigKeepsAccounts(t *testing.T) {
	path := configSandbox(t, accountsConfig)

	got, err := run(t, "config", "get", "accounts.0.key_env", "--config", path)
	if err != nil {
		t.Fatalf("config get accounts.0.key_env: %v\n%s", err, got)
	}
	if !strings.Contains(got, "TEAM_A_API_KEY") {
		t.Errorf("accounts.0.key_env 不对:\n%s", got)
	}

	shown, err := run(t, "config", "show", "--config", path, "--format", "yaml")
	if err != nil {
		t.Fatalf("config show: %v\n%s", err, shown)
	}
	for _, want := range []string{"accounts:", "team-a", "name: pool"} {
		if !strings.Contains(shown, want) {
			t.Errorf("config show 里没有 %q:\n%s", want, shown)
		}
	}
	if strings.Contains(shown, "pool/team-a") {
		t.Errorf("config show 不该出现展开名:\n%s", shown)
	}

	checked, err := run(t, "config", "validate", "--config", path, "--json")
	if err != nil {
		t.Fatalf("config validate: %v\n%s", err, checked)
	}
	var result configCheck
	if err := json.Unmarshal([]byte(checked), &result); err != nil {
		t.Fatalf("输出不是合法 JSON: %v\n%s", err, checked)
	}
	if result.Channels != 3 || result.Models != 1 {
		t.Errorf("校验报的计数应当是文件里的 3 条渠道 1 条规则，实际 %d / %d",
			result.Channels, result.Models)
	}
}

// TestManageChannelDeleteUsesFilePositions 验证交互管理的删除也按文件里的位置：
// 下标 1 是 plain-a，不是展开之后的第二条。
func TestManageChannelDeleteUsesFilePositions(t *testing.T) {
	path := configSandbox(t, accountsConfig)
	env, err := configmgr.Load(path)
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}

	removed, err := removeChannelAt(env, 1)
	if err != nil {
		t.Fatalf("removeChannelAt: %v", err)
	}
	if removed != "plain-a" {
		t.Errorf("下标 1 应当删掉 plain-a，实际是 %q", removed)
	}

	data := readFile(t, path)
	if strings.Contains(data, "name: plain-a") {
		t.Errorf("plain-a 应当被删掉:\n%s", data)
	}
	if !strings.Contains(data, "name: plain-b") {
		t.Errorf("plain-b 必须原样保留:\n%s", data)
	}
}

// TestManageChannelEditUsesFilePositions 验证交互编辑改的是文件里的那一条：
// 下标 1 是 plain-a，plain-b 的基址不受影响。
func TestManageChannelEditUsesFilePositions(t *testing.T) {
	path := configSandbox(t, accountsConfig)
	env, err := configmgr.Load(path)
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}
	if env.Config.Channels[1].Name != "plain-a" {
		t.Fatalf("下标 1 应当是 plain-a: %+v", env.Config.Channels[1])
	}

	entry := channelEntryOf(env.Config.Channels[1])
	entry.BaseURL = "https://changed.example.com/v1"
	if err := applyChannelEdit(env, 1, entry); err != nil {
		t.Fatalf("applyChannelEdit: %v", err)
	}

	data := readFile(t, path)
	if !strings.Contains(data, "https://changed.example.com/v1") {
		t.Errorf("plain-a 的基址应当已经改写:\n%s", data)
	}
	if !strings.Contains(data, "https://c.example.com/v1") {
		t.Errorf("plain-b 的基址不该被动:\n%s", data)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("改写后的配置不合法: %v", err)
	}
	if channel, ok := cfg.ChannelByName("plain-b"); !ok || channel.BaseURL != "https://c.example.com/v1" {
		t.Errorf("plain-b 被改动了: %+v", channel)
	}
}

// TestImportWithAccountsSkipsBaseNameCollision 验证导入按文件里的渠道名判重，
// 不会又写出一条 pool。
func TestImportWithAccountsSkipsBaseNameCollision(t *testing.T) {
	path := configSandbox(t, accountsConfig)
	file := importFile(t, `{"success":true,"data":{"items":[
		{"id":1,"name":"pool","type":1,"base_url":"https://p.example.com","models":"p-1"}
	]}}`)

	out, err := run(t, "import", "--from", "new-api", "--file", file, "--config", path)
	if err != nil {
		t.Fatalf("import: %v\n%s", err, out)
	}
	if !strings.Contains(out, "已有同名渠道") {
		t.Errorf("导入应当按文件里的 pool 跳过:\n%s", out)
	}
	if data := readFile(t, path); strings.Count(data, "name: pool") != 1 {
		t.Errorf("文件里应当只有一条 pool:\n%s", data)
	}
}

// TestImportWithAccountsReservesAccountKeyEnv 验证导入生成的凭据变量名会避开账号
// 已经占用的 key_env。
func TestImportWithAccountsReservesAccountKeyEnv(t *testing.T) {
	path := configSandbox(t, accountsConfig)
	file := importFile(t, `{"success":true,"data":{"items":[
		{"id":1,"name":"team-a","type":1,"base_url":"https://t.example.com","models":"t-1"}
	]}}`)

	out, err := run(t, "import", "--from", "new-api", "--file", file, "--config", path)
	if err != nil {
		t.Fatalf("import: %v\n%s", err, out)
	}
	if !strings.Contains(out, "TEAM_A_API_KEY_2") {
		t.Errorf("导入应当避开账号占用的 TEAM_A_API_KEY:\n%s", out)
	}
	if data := readFile(t, path); !strings.Contains(data, "key_env: TEAM_A_API_KEY_2") {
		t.Errorf("写出的配置应当用避开后的变量名:\n%s", data)
	}
}

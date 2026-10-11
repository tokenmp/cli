package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/cobra"

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

// accountsSpareConfig 是 accounts 命令的沙箱：team-a 与 team-b 被 pool 引用，
// spare 没有被任何渠道引用（core 会因此拒绝加载，accounts 命令按中间状态处理）。
const accountsSpareConfig = `# 顶层账号沙箱配置
listen: 127.0.0.1:9999

accounts:
  # 团队 A
  - {name: team-a, key_env: TEAM_A_API_KEY, headers: {x-workspace: team-a}}
  - {name: team-b, key_env: TEAM_B_API_KEY}
  - {name: spare, key_env: SPARE_API_KEY}

channels:
  - name: pool
    dialect: openai
    base_url: "https://b.example.com/v1"
    path: /chat/completions
    auth: {style: bearer, accounts: [team-a, team-b]}

models:
  - {match: "m-*", channel: pool}
`

// TestAccountsListShowsReferences 验证列表按文件顺序列出账号，并标出被哪几条渠道引用。
func TestAccountsListShowsReferences(t *testing.T) {
	path := configSandbox(t, accountsSpareConfig)

	out, err := run(t, "accounts", "list", "--config", path)
	if err != nil {
		t.Fatalf("accounts list: %v\n%s", err, out)
	}
	for _, want := range []string{"team-a", "team-b", "spare", "pool"} {
		if !strings.Contains(out, want) {
			t.Errorf("账号表里没有 %q:\n%s", want, out)
		}
	}

	jsonOut, err := run(t, "accounts", "list", "--config", path, "--json")
	if err != nil {
		t.Fatalf("accounts list --json: %v\n%s", err, jsonOut)
	}
	var rows []accountRow
	if err := json.Unmarshal([]byte(jsonOut), &rows); err != nil {
		t.Fatalf("输出不是合法 JSON: %v\n%s", err, jsonOut)
	}
	if len(rows) != 3 {
		t.Fatalf("应当有 3 个账号，实际 %d 个: %+v", len(rows), rows)
	}
	if rows[0].Name != "team-a" || rows[1].Name != "team-b" || rows[2].Name != "spare" {
		t.Errorf("顺序应当与文件一致: %+v", rows)
	}
	if len(rows[0].Channels) != 1 || rows[0].Channels[0] != "pool" {
		t.Errorf("team-a 应当被 pool 引用: %+v", rows[0])
	}
	if len(rows[2].Channels) != 0 {
		t.Errorf("spare 不该有引用渠道: %+v", rows[2])
	}
	if rows[0].Headers != 1 {
		t.Errorf("team-a 应当有 1 个请求头: %+v", rows[0])
	}
}

// TestAccountsListCredentialStates 验证凭据状态按 key_env 有没有设置给出。
func TestAccountsListCredentialStates(t *testing.T) {
	path := configSandbox(t, accountsSpareConfig)

	states := func(t *testing.T) map[string]string {
		t.Helper()
		out, err := run(t, "accounts", "list", "--config", path, "--json")
		if err != nil {
			t.Fatalf("accounts list --json: %v\n%s", err, out)
		}
		var rows []accountRow
		if err := json.Unmarshal([]byte(out), &rows); err != nil {
			t.Fatalf("输出不是合法 JSON: %v\n%s", err, out)
		}
		result := make(map[string]string, len(rows))
		for _, row := range rows {
			result[row.Name] = row.Credential
		}
		return result
	}

	if got := states(t)["team-a"]; got != credentialMissing {
		t.Errorf("没设环境变量时应当是 %q，实际 %q", credentialMissing, got)
	}
	t.Setenv("TEAM_A_API_KEY", "a")
	if got := states(t)["team-a"]; got != credentialPresent {
		t.Errorf("设了环境变量后应当是 %q，实际 %q", credentialPresent, got)
	}
}

// TestAccountsGroupFallsBackToList 验证非终端下 accounts 组与 accounts list 输出一致。
func TestAccountsGroupFallsBackToList(t *testing.T) {
	path := configSandbox(t, accountsSpareConfig)

	group, err := run(t, "accounts", "--config", path)
	if err != nil {
		t.Fatalf("accounts: %v\n%s", err, group)
	}
	list, err := run(t, "accounts", "list", "--config", path)
	if err != nil {
		t.Fatalf("accounts list: %v\n%s", err, list)
	}
	if group != list {
		t.Errorf("非终端时 accounts 应当与 accounts list 一致\naccounts:\n%s\nlist:\n%s", group, list)
	}
}

// TestAccountsGroupJSONNonTTY 验证管道里给 --json 打印 JSON，而不是报互斥错误。
func TestAccountsGroupJSONNonTTY(t *testing.T) {
	path := configSandbox(t, accountsSpareConfig)

	group, err := run(t, "accounts", "--config", path, "--json")
	if err != nil {
		t.Fatalf("accounts --json: %v\n%s", err, group)
	}
	list, err := run(t, "accounts", "list", "--config", path, "--json")
	if err != nil {
		t.Fatalf("accounts list --json: %v\n%s", err, list)
	}
	if group != list {
		t.Errorf("两处 --json 输出应当一致\naccounts:\n%s\nlist:\n%s", group, list)
	}

	var rows []accountRow
	if err := json.Unmarshal([]byte(group), &rows); err != nil {
		t.Fatalf("输出不是 JSON 数组: %v\n%s", err, group)
	}
	if len(rows) != 3 {
		t.Errorf("JSON 里有 %d 个账号，想要 3 个", len(rows))
	}
}

// TestAccountsGroupJSONConflictsWithInteractive 验证进交互时 --json 报错。
func TestAccountsGroupJSONConflictsWithInteractive(t *testing.T) {
	path := configSandbox(t, accountsSpareConfig)

	if err := runAccounts(&cobra.Command{}, &accountsOptions{configPath: path, jsonOut: true}, true); err == nil {
		t.Error("accounts 进交互时给 --json 应当报错")
	} else if !strings.Contains(err.Error(), "--json") {
		t.Errorf("错误信息里应当点出 --json: %v", err)
	}
}

// TestAccountsHelpStillWorks 验证 --help 与 help 仍然给帮助，不被交互挡住。
func TestAccountsHelpStillWorks(t *testing.T) {
	for _, args := range [][]string{{"accounts", "--help"}, {"help", "accounts"}} {
		out, err := run(t, args...)
		if err != nil {
			t.Fatalf("tokenmp %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		for _, want := range []string{"accounts", "分步管理"} {
			if !strings.Contains(out, want) {
				t.Errorf("tokenmp %s 的帮助里应当有 %q:\n%s", strings.Join(args, " "), want, out)
			}
		}
	}
}

// TestAccountsCreateAppendsAndPreservesComments 验证 create 追加到顶层 accounts 段，
// 注释保留，--header 写成账号级请求头。
func TestAccountsCreateAppendsAndPreservesComments(t *testing.T) {
	path := configSandbox(t, accountsSpareConfig)

	out, err := run(t, "accounts", "create",
		"--name", "team-c",
		"--key-env", "TEAM_C_API_KEY",
		"--header", "x-workspace=team-c",
		"--header", "x-org=o1",
		"--config", path,
	)
	if err != nil {
		t.Fatalf("accounts create: %v\n%s", err, out)
	}

	data := readFile(t, path)
	for _, want := range []string{"# 顶层账号沙箱配置", "# 团队 A", "name: team-c", "key_env: TEAM_C_API_KEY", "x-workspace: team-c", "x-org: o1"} {
		if !strings.Contains(data, want) {
			t.Errorf("改写结果里没有 %q:\n%s", want, data)
		}
	}

	env, err := configmgr.LoadAccounts(path)
	if err != nil {
		t.Fatalf("写完的配置读不回来: %v", err)
	}
	if len(env.Config.Accounts) != 4 {
		t.Fatalf("账号数量不对: %d", len(env.Config.Accounts))
	}
	created := env.Config.Accounts[3]
	if created.Name != "team-c" || created.KeyEnv != "TEAM_C_API_KEY" || created.Headers["x-workspace"] != "team-c" {
		t.Errorf("新账号不对: %+v", created)
	}
}

// TestAccountsCreateThenChannelReferencesLoads 验证先建账号、再用 channels create 接上
// 引用之后，配置能被 core 严格加载：这正是 accounts create 与 channels create --accounts
// 配合起来的完整路径。
func TestAccountsCreateThenChannelReferencesLoads(t *testing.T) {
	path := configSandbox(t, accountsConfig)

	if out, err := run(t, "accounts", "create", "--name", "team-c", "--key-env", "TEAM_C_API_KEY", "--config", path); err != nil {
		t.Fatalf("accounts create: %v\n%s", err, out)
	}
	if _, err := config.Load(path); err == nil {
		t.Fatal("账号还没被引用时 core 应当拒绝加载")
	}

	if out, err := run(t, "channels", "create",
		"--name", "pool-c",
		"--dialect", "openai",
		"--base-url", "https://c.example.com/v1",
		"--path", "/chat/completions",
		"--accounts", "team-c",
		"--config", path,
	); err != nil {
		t.Fatalf("channels create --accounts: %v\n%s", err, out)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("接上引用后 core 应当能加载: %v", err)
	}
	if _, ok := cfg.ChannelByName("pool-c/team-c"); !ok {
		t.Errorf("展开结果里应当有 pool-c/team-c: %+v", cfg.Channels)
	}
	fileCfg, err := configmgr.Load(path)
	if err != nil {
		t.Fatalf("按文件原样加载: %v", err)
	}
	poolCopy, ok := fileCfg.Config.ChannelByName("pool-c")
	if !ok || len(poolCopy.Auth.Accounts) != 1 || poolCopy.Auth.Accounts[0] != "team-c" {
		t.Errorf("渠道条目里应当写出账号引用: %+v", poolCopy.Auth)
	}
}

// TestAccountsCreateRejectsDuplicate 验证重名报错且不覆盖。
func TestAccountsCreateRejectsDuplicate(t *testing.T) {
	path := configSandbox(t, accountsSpareConfig)
	before := readFile(t, path)

	out, err := run(t, "accounts", "create", "--name", "team-a", "--key-env", "OTHER_KEY", "--config", path)
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

// TestAccountsCreateRejectsBadInput 验证必填与 --header 写法。
func TestAccountsCreateRejectsBadInput(t *testing.T) {
	path := configSandbox(t, accountsSpareConfig)
	before := readFile(t, path)

	cases := []struct {
		name string
		args []string
		want string
	}{
		{"缺 name", []string{"accounts", "create", "--key-env", "K", "--config", path}, "--name"},
		{"缺 key-env", []string{"accounts", "create", "--name", "team-c", "--config", path}, "--key-env"},
		{"header 写法不对", []string{"accounts", "create", "--name", "team-c", "--key-env", "K", "--header", "foo", "--config", path}, "k=v"},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			out, err := run(t, item.args...)
			if err == nil {
				t.Fatalf("%s 应当报错\n%s", item.name, out)
			}
			if !strings.Contains(out, item.want) {
				t.Errorf("错误信息里应当有 %q:\n%s", item.want, out)
			}
		})
	}
	if readFile(t, path) != before {
		t.Error("失败后文件不该改动")
	}
}

// TestAccountsCreateDryRunDoesNotWrite 验证 --dry-run 打印条目但不落盘。
func TestAccountsCreateDryRunDoesNotWrite(t *testing.T) {
	path := configSandbox(t, accountsSpareConfig)
	before := readFile(t, path)

	out, err := run(t, "accounts", "create", "--name", "team-c", "--key-env", "TEAM_C_API_KEY", "--header", "x-workspace=team-c", "--config", path, "--dry-run")
	if err != nil {
		t.Fatalf("accounts create --dry-run: %v\n%s", err, out)
	}
	for _, want := range []string{"将要追加", "team-c", "TEAM_C_API_KEY", "x-workspace: team-c"} {
		if !strings.Contains(out, want) {
			t.Errorf("dry-run 输出里应当有 %q:\n%s", want, out)
		}
	}
	if readFile(t, path) != before {
		t.Error("--dry-run 不该落盘")
	}
}

// TestAccountsDeleteRefusesReferenced 验证仍被渠道引用时拒绝，并列出渠道与下标。
func TestAccountsDeleteRefusesReferenced(t *testing.T) {
	path := configSandbox(t, accountsSpareConfig)
	before := readFile(t, path)

	out, err := run(t, "accounts", "delete", "team-a", "--config", path)
	if err == nil {
		t.Fatal("删除被引用的账号应当报错")
	}
	for _, want := range []string{"仍被", "channels[0]", "pool"} {
		if !strings.Contains(out, want) {
			t.Errorf("拒绝信息里应当包含 %q:\n%s", want, out)
		}
	}
	if readFile(t, path) != before {
		t.Error("被拒绝后文件不该改动")
	}
}

// TestAccountsDeleteUnreferencedSucceeds 验证删掉没有被引用的账号时成功。
func TestAccountsDeleteUnreferencedSucceeds(t *testing.T) {
	path := configSandbox(t, accountsSpareConfig)

	out, err := run(t, "accounts", "delete", "spare", "--config", path)
	if err != nil {
		t.Fatalf("accounts delete spare: %v\n%s", err, out)
	}
	if !strings.Contains(out, "accounts.2") {
		t.Errorf("删除结果应当报 accounts.2:\n%s", out)
	}
	if data := readFile(t, path); strings.Contains(data, "spare") {
		t.Errorf("spare 应当被删掉:\n%s", data)
	}

	env, err := configmgr.LoadAccounts(path)
	if err != nil {
		t.Fatalf("删除后配置读不回来: %v", err)
	}
	if len(env.Config.Accounts) != 2 {
		t.Errorf("应当剩 2 个账号，实际 %d 个", len(env.Config.Accounts))
	}
}

// TestAccountsDeleteMissing 验证删不存在的账号报错。
func TestAccountsDeleteMissing(t *testing.T) {
	path := configSandbox(t, accountsSpareConfig)

	out, err := run(t, "accounts", "delete", "absent", "--config", path)
	if err == nil {
		t.Fatal("删除不存在的账号应当报错")
	}
	if !strings.Contains(out, "没有账号") {
		t.Errorf("错误信息里应当说明账号不存在:\n%s", out)
	}
}

// TestChannelsCreateAccountsMutuallyExclusiveWithKeyEnv 验证两个凭据来源互斥。
func TestChannelsCreateAccountsMutuallyExclusiveWithKeyEnv(t *testing.T) {
	path := configSandbox(t, accountsConfig)
	before := readFile(t, path)

	out, err := run(t, "channels", "create",
		"--name", "both",
		"--dialect", "openai",
		"--base-url", "https://x.example.com/v1",
		"--path", "/chat/completions",
		"--key-env", "KEY",
		"--accounts", "team-a",
		"--config", path,
	)
	if err == nil {
		t.Fatal("同时给 --key-env 与 --accounts 应当报错")
	}
	if !strings.Contains(out, "互斥") {
		t.Errorf("错误信息里应当说明互斥:\n%s", out)
	}
	if readFile(t, path) != before {
		t.Error("失败后文件不该改动")
	}
}

// TestChannelsCreateAccountsRejectsMissing 验证引用不存在的账号时报错，文案与 core 一致。
func TestChannelsCreateAccountsRejectsMissing(t *testing.T) {
	path := configSandbox(t, accountsConfig)

	out, err := run(t, "channels", "create",
		"--name", "bad",
		"--dialect", "openai",
		"--base-url", "https://x.example.com/v1",
		"--path", "/chat/completions",
		"--accounts", "team-z",
		"--config", path,
	)
	if err == nil {
		t.Fatal("引用不存在的账号应当报错")
	}
	if !strings.Contains(out, "引用了不存在的账号 team-z") {
		t.Errorf("错误信息应当与 core 一致:\n%s", out)
	}
}

// TestChannelsCreateAccountsWrites 验证渠道的 auth.accounts 写回并能被 core 加载。
func TestChannelsCreateAccountsWrites(t *testing.T) {
	path := configSandbox(t, accountsConfig)

	out, err := run(t, "channels", "create",
		"--name", "pool-copy",
		"--dialect", "openai",
		"--base-url", "https://c.example.com/v1",
		"--path", "/chat/completions",
		"--accounts", "team-a, team-b",
		"--config", path,
	)
	if err != nil {
		t.Fatalf("channels create --accounts: %v\n%s", err, out)
	}

	if data := readFile(t, path); !strings.Contains(data, "accounts:") {
		t.Errorf("渠道条目里应当写出账号引用:\n%s", data)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("改写后的配置不合法: %v", err)
	}
	for _, name := range []string{"pool-copy/team-a", "pool-copy/team-b"} {
		if _, ok := cfg.ChannelByName(name); !ok {
			t.Errorf("展开结果里应当有 %s", name)
		}
	}
	fileCfg, err := configmgr.Load(path)
	if err != nil {
		t.Fatalf("按文件原样加载: %v", err)
	}
	copyChannel, ok := fileCfg.Config.ChannelByName("pool-copy")
	if !ok || len(copyChannel.Auth.Accounts) != 2 {
		t.Errorf("渠道条目里应当写出两条账号引用: %+v", copyChannel.Auth)
	}
}

// TestAccountsCreateFirstAccount 验证配置里还没有 accounts 段时也能追加出第一段。
func TestAccountsCreateFirstAccount(t *testing.T) {
	path := configSandbox(t, emptyChannelsConfig)

	out, err := run(t, "accounts", "create", "--name", "team-a", "--key-env", "TEAM_A_API_KEY", "--config", path)
	if err != nil {
		t.Fatalf("accounts create: %v\n%s", err, out)
	}
	if data := readFile(t, path); !strings.Contains(data, "accounts:") || !strings.Contains(data, "name: team-a") {
		t.Errorf("应当补出 accounts 段并写入新账号:\n%s", data)
	}

	env, err := configmgr.LoadAccounts(path)
	if err != nil {
		t.Fatalf("写完的配置读不回来: %v", err)
	}
	if len(env.Config.Accounts) != 1 || env.Config.Accounts[0].Name != "team-a" {
		t.Errorf("新账号不对: %+v", env.Config.Accounts)
	}
}

// TestAccountsDeleteLastWritesEmptySequence 验证删到最后一个账号时写成 accounts: []。
func TestAccountsDeleteLastWritesEmptySequence(t *testing.T) {
	const loneAccountConfig = `listen: 127.0.0.1:9999

accounts:
  - {name: solo, key_env: SOLO_API_KEY}

channels: []

models: []
`
	path := configSandbox(t, loneAccountConfig)

	if out, err := run(t, "accounts", "delete", "solo", "--config", path); err != nil {
		t.Fatalf("accounts delete solo: %v\n%s", err, out)
	}
	if data := readFile(t, path); !strings.Contains(data, "accounts: []") {
		t.Errorf("删到最后一个账号应当写成 accounts: []:\n%s", data)
	}
	if _, err := config.Load(path); err != nil {
		t.Fatalf("删除后配置不合法: %v", err)
	}
}

// TestAccountEditFields 验证账号编辑只比出变化的字段，清空的请求头用 nil 删除。
func TestAccountEditFields(t *testing.T) {
	current := config.Account{Name: "team-a", KeyEnv: "TEAM_A_API_KEY", Headers: map[string]string{"x-workspace": "a"}}
	if fields := accountEditFields(current, accountEntryOf(current)); len(fields) != 0 {
		t.Errorf("没有变化时不该有字段: %+v", fields)
	}

	entry := accountEntryOf(current)
	entry.KeyEnv = "CHANGED"
	entry.Headers = nil

	got := make(map[string]any)
	for _, field := range accountEditFields(current, entry) {
		got[strings.Join(field.Path, ".")] = field.Value
	}
	if got["key_env"] != "CHANGED" {
		t.Errorf("key_env 应当变化: %+v", got)
	}
	if value, ok := got["headers"]; !ok || value != nil {
		t.Errorf("清空的 headers 应当用 nil 删除: %+v", got)
	}
	if _, ok := got["name"]; ok {
		t.Errorf("没变的 name 不该出现: %+v", got)
	}
}

// TestManageAccountEditWrites 验证交互路径的编辑只写变化字段并保留注释。
func TestManageAccountEditWrites(t *testing.T) {
	path := configSandbox(t, accountsSpareConfig)
	env, err := configmgr.LoadAccounts(path)
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}

	form := accountFormFromRecord(env.Config.Accounts[0])
	form.KeyEnv = "CHANGED_KEY"
	form.Headers = "x-workspace=a, x-org=o1"
	record, err := accountRecordFromForm(form)
	if err != nil {
		t.Fatalf("组装账号失败: %v", err)
	}
	if err := applyAccountEdit(env, 0, accountEntryOf(record)); err != nil {
		t.Fatalf("applyAccountEdit: %v", err)
	}

	data := readFile(t, path)
	for _, want := range []string{"# 团队 A", "key_env: CHANGED_KEY", "x-org: o1"} {
		if !strings.Contains(data, want) {
			t.Errorf("改写结果里没有 %q:\n%s", want, data)
		}
	}
}

// TestManageAccountDeleteRefusesReferenced 验证交互路径同样拒绝删除被引用的账号。
func TestManageAccountDeleteRefusesReferenced(t *testing.T) {
	path := configSandbox(t, accountsSpareConfig)
	env, err := configmgr.LoadAccounts(path)
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}
	before := readFile(t, path)

	if _, err := removeAccountAt(env, 0); err == nil {
		t.Fatal("删除被引用的账号应当报错")
	} else if !strings.Contains(err.Error(), "channels[0]") {
		t.Errorf("拒绝信息里应当带渠道下标: %v", err)
	}
	if readFile(t, path) != before {
		t.Error("被拒绝后文件不该改动")
	}
}

// TestManageAccountDeleteUnreferenced 验证交互路径能删掉没被引用的账号。
func TestManageAccountDeleteUnreferenced(t *testing.T) {
	path := configSandbox(t, accountsSpareConfig)
	env, err := configmgr.LoadAccounts(path)
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}

	removed, err := removeAccountAt(env, 2)
	if err != nil {
		t.Fatalf("removeAccountAt: %v", err)
	}
	if removed != "spare" {
		t.Errorf("删掉的应当是 spare，实际是 %q", removed)
	}
	if data := readFile(t, path); strings.Contains(data, "spare") {
		t.Errorf("文件里不该还有 spare:\n%s", data)
	}
}

// TestManageAccountItemsMatchRows 验证交互条目与列表同序、字段齐全。
func TestManageAccountItemsMatchRows(t *testing.T) {
	env, err := configmgr.LoadAccounts(configSandbox(t, accountsSpareConfig))
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}

	items := accountBrowseItems(env)
	if len(items) != len(env.Config.Accounts) {
		t.Fatalf("账号条目数量不对: %d", len(items))
	}
	if items[2].label != "spare" {
		t.Errorf("条目顺序不对: %+v", items)
	}
	if len(items[0].detail) != 5 {
		t.Errorf("账号详情应当有 5 个字段: %d", len(items[0].detail))
	}
}

// TestValidateAccountFields 验证账号字段的逐步校验。
func TestValidateAccountFields(t *testing.T) {
	if err := validateAccountName("  "); err == nil {
		t.Error("空账号名应当报错")
	}
	if err := validateAccountKeyEnv(""); err == nil {
		t.Error("空 key_env 应当报错")
	}
	if err := validateAccountHeaders("x-workspace=a, x-org=b"); err != nil {
		t.Errorf("合法请求头不该报错: %v", err)
	}
	if err := validateAccountHeaders("broken"); err == nil {
		t.Error("没有 = 的请求头应当报错")
	}
	if err := validateAccountHeaders(""); err != nil {
		t.Errorf("留空请求头不该报错: %v", err)
	}
}

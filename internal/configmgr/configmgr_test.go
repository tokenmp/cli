package configmgr

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	coreconfig "github.com/tokenmp/core/config"
)

// accountConfig 是带顶层账号的沙箱配置：渠道 pool 引用 team-a 与 team-b，
// 另有一条不引用账号的渠道与一条规则，相对 plugins.dir 用来验证路径归一。
const accountConfig = `# 带顶层账号的沙箱配置
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
  - name: plain
    dialect: openai
    base_url: "https://a.example.com/v1"
    path: /chat/completions
    auth: {style: bearer, key_env: PLAIN_API_KEY}

models:
  - {match: "m-*", channel: pool}

plugins:
  dir: plugins
`

// TestParsedConfigExpandsLikeCoreLoad 是防回归的兜底测试：这里按文件原样拼出来的那份
// 配置调 Expand 之后，必须与 core 的 config.Load 逐字段相等。
//
// core 的 config.Parse 现在是「解码、Defaults、Validate、Expand、再 Validate」，将来
// 往里面加步骤时，这份自己拼的解析要跟着改；两边不一致时这条会红。
func TestParsedConfigExpandsLikeCoreLoad(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(path, []byte(accountConfig), 0o600); err != nil {
		t.Fatalf("写配置失败: %v", err)
	}

	env, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// 先确认拿到的确实是文件原样：顶层账号还在，渠道名是基础名。
	if len(env.Config.Accounts) != 2 {
		t.Fatalf("按文件原样读应当保留 2 个账号，实际 %d 个", len(env.Config.Accounts))
	}
	if len(env.Config.Channels) != 2 || env.Config.Channels[0].Name != "pool" {
		t.Fatalf("按文件原样读的渠道名应当是文件里的写法: %+v", env.Config.Channels)
	}
	if len(env.Config.Channels[0].Auth.Accounts) != 2 {
		t.Fatalf("渠道上的账号引用应当保留: %+v", env.Config.Channels[0].Auth)
	}

	expanded := env.Config.Expand()
	want, err := coreconfig.Load(path)
	if err != nil {
		t.Fatalf("core config.Load: %v", err)
	}
	if !reflect.DeepEqual(expanded, want) {
		t.Errorf("Expand 的结果与 core config.Load 不一致\n--- Expand ---\n%+v\n--- core ---\n%+v", expanded, want)
	}
}

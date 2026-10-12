package configmgr

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// recordsConfig 是带 records 段的沙箱配置。其余字段取最小可校验的写法。
const recordsConfig = `listen: 127.0.0.1:9999

channels:
  - name: example
    dialect: openai
    base_url: https://api.example.com/v1
    auth: {style: none}

models:
  - {match: "gpt-*", channel: example}

plugins:
  dir: plugins

records:
  endpoint: "http://127.0.0.1:6789/api/internal/records"
  secret_env: "RECORDS_CALLBACK_SECRET"
  batch_size: 50
  flush_interval: 3s
`

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("写配置失败: %v", err)
	}
	return path
}

// TestRecordsSectionIsParsed cli 自己的 records 段要能被接住：core 的解码开着
// KnownFields，这一段要是没接住，配置文件直接加载失败。
func TestRecordsSectionIsParsed(t *testing.T) {
	env, err := Load(writeConfig(t, recordsConfig))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if env.Records == nil {
		t.Fatal("records 段没有被解析出来")
	}
	if env.Records.Endpoint != "http://127.0.0.1:6789/api/internal/records" {
		t.Errorf("endpoint 是 %q", env.Records.Endpoint)
	}
	if env.Records.SecretEnv != "RECORDS_CALLBACK_SECRET" {
		t.Errorf("secret_env 是 %q", env.Records.SecretEnv)
	}
	if env.Records.BatchSize != 50 {
		t.Errorf("batch_size 是 %d，期望 50", env.Records.BatchSize)
	}
	if env.Records.FlushInterval.Duration() != 3*time.Second {
		t.Errorf("flush_interval 是 %s，期望 3s", env.Records.FlushInterval)
	}
}

// TestRecordsDefaultsAreFilled 只写端点与密钥时，其余两项补默认值。
func TestRecordsDefaultsAreFilled(t *testing.T) {
	content := strings.Replace(recordsConfig,
		"  batch_size: 50\n  flush_interval: 3s\n", "", 1)
	env, err := Load(writeConfig(t, content))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if env.Records.BatchSize != 200 {
		t.Errorf("batch_size 默认值是 %d，期望 200", env.Records.BatchSize)
	}
	if env.Records.FlushInterval.Duration() != 5*time.Second {
		t.Errorf("flush_interval 默认值是 %s，期望 5s", env.Records.FlushInterval)
	}
}

// TestRecordsAbsent 整段没写时 Records 为 nil，config show 里也不出现 records。
func TestRecordsAbsent(t *testing.T) {
	content := recordsConfig[:strings.Index(recordsConfig, "\nrecords:")] + "\n"
	env, err := Load(writeConfig(t, content))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if env.Records != nil {
		t.Fatalf("没写 records 段时不该有值: %+v", env.Records)
	}
	pruned, err := env.Pruned()
	if err != nil {
		t.Fatalf("Pruned: %v", err)
	}
	if _, ok := pruned["records"]; ok {
		t.Errorf("config show 里不该出现 records: %+v", pruned)
	}
}

// TestRecordsShownInConfigShow 写了 records 段时，config show 的形状里要带上它。
func TestRecordsShownInConfigShow(t *testing.T) {
	env, err := Load(writeConfig(t, recordsConfig))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	pruned, err := env.Pruned()
	if err != nil {
		t.Fatalf("Pruned: %v", err)
	}
	section, ok := pruned["records"].(map[string]any)
	if !ok {
		t.Fatalf("config show 里没有 records: %+v", pruned)
	}
	if section["endpoint"] != "http://127.0.0.1:6789/api/internal/records" {
		t.Errorf("records.endpoint 是 %v", section["endpoint"])
	}
	if section["flush_interval"] != "3s" {
		t.Errorf("records.flush_interval 是 %v，期望 3s", section["flush_interval"])
	}
}

// TestRecordsTypoIsRejected KnownFields 对 records 段内部同样生效。
func TestRecordsTypoIsRejected(t *testing.T) {
	content := strings.Replace(recordsConfig, "batch_size: 50", "batch_sizes: 50", 1)
	if _, err := Load(writeConfig(t, content)); err == nil {
		t.Fatal("records 段里拼错的字段应当报错")
	}
}

// TestRecordsInvalidEndpointIsRejected 端点协议不对时要拦住，写回校验也走同一条路。
func TestRecordsInvalidEndpointIsRejected(t *testing.T) {
	content := strings.Replace(recordsConfig,
		`endpoint: "http://127.0.0.1:6789/api/internal/records"`, `endpoint: "127.0.0.1:6789"`, 1)
	path := writeConfig(t, content)
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "http:// 或 https://") {
		t.Fatalf("期望报错含协议说明，实际 %v", err)
	}
	if err := Validate(path); err == nil {
		t.Fatal("Validate 也应当拦下不合法的 records 段")
	}
}

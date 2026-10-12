package records

import (
	"strings"
	"testing"
	"time"
)

func TestConfigDefaults(t *testing.T) {
	cfg := Config{Endpoint: "http://127.0.0.1:6789/api/internal/records"}
	cfg.Defaults()
	if cfg.BatchSize != DefaultBatchSize {
		t.Errorf("batch_size 默认值是 %d，期望 %d", cfg.BatchSize, DefaultBatchSize)
	}
	if cfg.FlushInterval.Duration() != DefaultFlushInterval {
		t.Errorf("flush_interval 默认值是 %s，期望 %s", cfg.FlushInterval, DefaultFlushInterval)
	}
	if cfg.CacheTTL.Duration() != DefaultCacheTTL {
		t.Errorf("cache_ttl 默认值是 %s，期望 %s", cfg.CacheTTL, DefaultCacheTTL)
	}
}

func TestConfigValidate(t *testing.T) {
	cases := []struct {
		name    string
		cfg     Config
		wantErr string
	}{
		{
			name: "合法",
			cfg:  Config{Endpoint: "http://127.0.0.1:6789/api/internal/records", SecretEnv: "RECORDS_CALLBACK_SECRET"},
		},
		{
			name:    "端点为空",
			cfg:     Config{SecretEnv: "RECORDS_CALLBACK_SECRET"},
			wantErr: "endpoint 不能为空",
		},
		{
			name:    "端点协议不对",
			cfg:     Config{Endpoint: "127.0.0.1:6789", SecretEnv: "RECORDS_CALLBACK_SECRET"},
			wantErr: "http:// 或 https://",
		},
		{
			name:    "密钥变量名为空",
			cfg:     Config{Endpoint: "http://127.0.0.1:6789"},
			wantErr: "secret_env 不能为空",
		},
		{
			name: "校验端点协议不对",
			cfg: Config{
				Endpoint:     "http://127.0.0.1:6789/api/internal/records",
				SecretEnv:    "RECORDS_CALLBACK_SECRET",
				AuthEndpoint: "127.0.0.1:6789/api/internal/api-key",
			},
			wantErr: "auth_endpoint 必须是 http:// 或 https://",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := tc.cfg
			cfg.Defaults()
			err := cfg.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("期望通过校验，实际 %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("期望报错含 %q，实际 %v", tc.wantErr, err)
			}
		})
	}
}

func TestSecretFromEnv(t *testing.T) {
	t.Setenv("RECORDS_TEST_SECRET", "s3cr3t")

	cfg := Config{SecretEnv: "RECORDS_TEST_SECRET"}
	got, err := cfg.SecretFromEnv()
	if err != nil {
		t.Fatalf("读取密钥失败: %v", err)
	}
	if got != "s3cr3t" {
		t.Errorf("密钥是 %q，期望 s3cr3t", got)
	}

	missing := Config{SecretEnv: "RECORDS_TEST_MISSING"}
	if _, err := missing.SecretFromEnv(); err == nil {
		t.Error("环境变量没设置时应当报错")
	}
}

// TestConfigAuthURL 校验端点默认从摄入端点推出，显式写了就用写的那个。
func TestConfigAuthURL(t *testing.T) {
	cases := []struct {
		name     string
		endpoint string
		auth     string
		want     string
	}{
		{
			name:     "从摄入端点推出",
			endpoint: "http://127.0.0.1:6789/api/internal/records",
			want:     "http://127.0.0.1:6789/api/internal/api-key",
		},
		{
			name:     "摄入端点带尾斜杠",
			endpoint: "http://127.0.0.1:6789/api/internal/records/",
			want:     "http://127.0.0.1:6789/api/internal/api-key",
		},
		{
			name:     "显式写了就用写的",
			endpoint: "http://127.0.0.1:6789/api/internal/records",
			auth:     "https://web.example.com/api/internal/api-key",
			want:     "https://web.example.com/api/internal/api-key",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{Endpoint: tc.endpoint, AuthEndpoint: tc.auth}
			if got := cfg.AuthURL(); got != tc.want {
				t.Errorf("校验端点地址是 %q，期望 %q", got, tc.want)
			}
		})
	}
}

// TestFailClosedEnabled 没写 fail_closed 时默认拒绝。
func TestFailClosedEnabled(t *testing.T) {
	if !(&Config{}).FailClosedEnabled() {
		t.Error("没写 fail_closed 时应当按拒绝处理")
	}
	enabled := true
	if !(&Config{FailClosed: &enabled}).FailClosedEnabled() {
		t.Error("fail_closed 为 true 时应当拒绝")
	}
	disabled := false
	if (&Config{FailClosed: &disabled}).FailClosedEnabled() {
		t.Error("fail_closed 为 false 时应当放行")
	}
}

func TestEnabled(t *testing.T) {
	var absent *Config
	if absent.Enabled() {
		t.Error("records 段没写时不该推送")
	}
	if (&Config{}).Enabled() {
		t.Error("端点为空的 records 段不该推送")
	}
	if !(&Config{Endpoint: "http://127.0.0.1:6789"}).Enabled() {
		t.Error("配了端点的 records 段应当推送")
	}
	if DefaultFlushInterval != 5*time.Second {
		t.Errorf("兜底发送间隔是 %s，与对接文档约定的 5s 不符", DefaultFlushInterval)
	}
}

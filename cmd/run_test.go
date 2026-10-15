package cmd

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tokenmp/core/dialect"
)

// runnableConfig 是一份能真正启动的配置：渠道不鉴权，避免启动时要求凭据环境变量。
const runnableConfig = `listen: 127.0.0.1:9999

log:
  format: text
  level: info

channels:
  - name: example
    dialect: %s
    base_url: %s
    auth:
      style: none
    timeout: 5s

models:
  - match: "gpt-*"
    channel: example

plugins:
  dir: plugins
`

// addressPattern 从启动信息里取监听地址。测试里输出不是终端，因此没有颜色码。
var addressPattern = regexp.MustCompile(`监听:\s+(\S+)`)

// syncBuffer 是并发安全的缓冲区。服务在后台写，测试在主 goroutine 里读。
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestRunDryRun(t *testing.T) {
	path := configSandbox(t, fmt.Sprintf(runnableConfig, "openai", "https://api.example.com"))

	out, err := run(t, "run", "--dry-run", "--config", path)
	if err != nil {
		t.Fatalf("run --dry-run: %v\n%s", err, out)
	}
	for _, want := range []string{"监听:", "127.0.0.1:9999", "渠道:", "1 个", "模型规则:", "插件目录:", "没有开始监听"} {
		if !strings.Contains(out, want) {
			t.Errorf("启动信息里没有 %q:\n%s", want, out)
		}
	}
}

// TestRunSummaryListsCountTokens 启动信息里的端点清单要有计数端点。
//
// 清单是从 dialect 的常量拼的，core 加了端点而这里漏掉时，使用者按启动信息核对
// 端口会被误导：他会以为这个路径没开。
func TestRunSummaryListsCountTokens(t *testing.T) {
	path := configSandbox(t, fmt.Sprintf(runnableConfig, "claude", "https://api.example.com"))

	out, err := run(t, "run", "--dry-run", "--config", path)
	if err != nil {
		t.Fatalf("run --dry-run: %v\n%s", err, out)
	}
	if !strings.Contains(out, dialect.ClaudeCountTokensPath) {
		t.Errorf("启动信息的端点清单里没有 %s:\n%s", dialect.ClaudeCountTokensPath, out)
	}
}

func TestRunListenOverride(t *testing.T) {
	path := configSandbox(t, fmt.Sprintf(runnableConfig, "openai", "https://api.example.com"))

	out, err := run(t, "run", "--dry-run", "--config", path, "--listen", "127.0.0.1:9000")
	if err != nil {
		t.Fatalf("run --dry-run --listen: %v\n%s", err, out)
	}
	if !strings.Contains(out, "127.0.0.1:9000") {
		t.Errorf("--listen 没有覆盖配置里的地址:\n%s", out)
	}
	if strings.Contains(out, "127.0.0.1:9999") {
		t.Errorf("配置里的地址不该出现:\n%s", out)
	}
}

func TestRunInitSkeletonStarts(t *testing.T) {
	root := t.TempDir()
	path := root + "/config.yaml"
	if _, err := run(t, "config", "init", "--config", path); err != nil {
		t.Fatalf("config init: %v", err)
	}

	// 骨架的渠道用了凭据环境变量，启动时会要求它存在。
	t.Setenv("EXAMPLE_API_KEY", "test-key")

	out, err := run(t, "run", "--dry-run", "--config", path)
	if err != nil {
		t.Fatalf("config init 生成的骨架应当能启动: %v\n%s", err, out)
	}
}

func TestRunMissingConfig(t *testing.T) {
	_, err := run(t, "run", "--config", t.TempDir()+"/absent.yaml")
	if err == nil {
		t.Fatal("配置文件不存在时应当报错")
	}
	if !strings.Contains(err.Error(), "不存在") {
		t.Errorf("报错应当说明配置不存在: %v", err)
	}
}

// TestRunServes 起一个真服务：请求 /healthz，再把 Claude 方言的请求转发给一个假的
// OpenAI 上游，验证配置、渠道、转换与响应这条链路。
func TestRunServes(t *testing.T) {
	var (
		mu       sync.Mutex
		upstream []byte
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		upstream = body
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"chatcmpl-1","object":"chat.completion","model":"gpt-4o",`+
			`"choices":[{"index":0,"message":{"role":"assistant","content":"来自假上游"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()

	path := configSandbox(t, fmt.Sprintf(runnableConfig, "openai", server.URL))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	out := &syncBuffer{}
	root := NewRootCmd()
	root.SetOut(out)
	root.SetErr(out)
	root.SetArgs([]string{"run", "--config", path, "--listen", "127.0.0.1:0"})

	done := make(chan error, 1)
	go func() { done <- root.ExecuteContext(ctx) }()

	address := waitForAddress(t, out)

	response, err := http.Get("http://" + address + "/healthz")
	if err != nil {
		t.Fatalf("请求 /healthz 失败: %v\n%s", err, out)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Errorf("/healthz 的状态码是 %d，期望 200", response.StatusCode)
	}

	// 客户端说 Claude 方言，渠道说 OpenAI，这条请求要经过转换。
	claudeRequest := `{"model":"gpt-4o","max_tokens":64,"messages":[{"role":"user","content":"你好"}]}`
	response, err = http.Post("http://"+address+"/v1/messages", "application/json", strings.NewReader(claudeRequest))
	if err != nil {
		t.Fatalf("转发请求失败: %v\n%s", err, out)
	}
	defer func() { _ = response.Body.Close() }()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("转发请求的状态码是 %d: %s", response.StatusCode, body)
	}
	if !strings.Contains(string(body), "来自假上游") {
		t.Errorf("响应里没有上游的内容: %s", body)
	}

	mu.Lock()
	forwarded := string(upstream)
	mu.Unlock()
	if !strings.Contains(forwarded, `"model":"gpt-4o"`) || !strings.Contains(forwarded, `"messages"`) {
		t.Errorf("上游收到的不是 OpenAI 形状的请求: %s", forwarded)
	}

	// 取消 context 相当于收到中断信号，服务应当干净退出。
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("退出时报错: %v\n%s", err, out)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("取消之后服务没有退出")
	}
	if !strings.Contains(out.String(), "服务已停止") {
		t.Errorf("退出时没有确认停止:\n%s", out)
	}
}

// waitForAddress 等启动信息里的监听地址出现。
func waitForAddress(t *testing.T, out *syncBuffer) string {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if match := addressPattern.FindStringSubmatch(out.String()); match != nil {
			return match[1]
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("启动信息里没有监听地址:\n%s", out)
	return ""
}

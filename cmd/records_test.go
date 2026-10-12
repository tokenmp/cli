package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// recordsSection 是追加到 runnableConfig 后面的 records 段。endpoint 传完整地址，
// 校验端点由 records.Config.AuthURL 从它推出（把结尾的 /records 换成 /api-key）。
const recordsSection = `
records:
  endpoint: "%s"
  secret_env: "TOKENMP_RECORDS_SECRET"
  batch_size: 10
  flush_interval: 5s
`

// 内部端点的路径。web 的两个端点路径固定，测试里按路径分流。
const (
	recordsPath = "/api/internal/records"
	apiKeyPath  = "/api/internal/api-key"
)

// upstreamWithUsage 是一个返回带用量响应的假上游。
func upstreamWithUsage(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"chatcmpl-1","object":"chat.completion","model":"gpt-4o",`+
			`"choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],`+
			`"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`)
	}))
	t.Cleanup(server.Close)
	return server
}

// startRun 在后台起一条 run 命令，返回监听地址、输出缓冲与结束通道。
func startRun(t *testing.T, path string) (string, *syncBuffer, chan error, context.CancelFunc) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	out := &syncBuffer{}
	root := NewRootCmd()
	root.SetOut(out)
	root.SetErr(out)
	root.SetArgs([]string{"run", "--config", path, "--listen", "127.0.0.1:0"})

	done := make(chan error, 1)
	go func() { done <- root.ExecuteContext(ctx) }()
	address := waitForAddress(t, out)
	return address, out, done, cancel
}

// TestRunPushesCallRecords 配了 records 段之后，一条请求在摄入端点落成一行，
// 字段与对接文档对得上。
func TestRunPushesCallRecords(t *testing.T) {
	t.Setenv("TOKENMP_RECORDS_SECRET", "test-secret")

	type batch struct {
		Secret  string       `json:"-"`
		Raw     string       `json:"-"`
		Records []recordsRow `json:"records"`
	}
	received := make(chan batch, 4)
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == apiKeyPath {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"ok":true,"data":{"user_id":263,"api_key_id":42,`+
				`"model_whitelist":null,"rate_limit_per_min":60,"expires_at":null,`+
				`"pricing":{"plan_id":3,"pricing_mode":"markup","markup_multiplier":1.15,"prices":[]}}}`)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var payload batch
		_ = json.Unmarshal(body, &payload)
		payload.Secret = r.Header.Get("x-callback-secret")
		payload.Raw = string(body)
		received <- payload
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(hook.Close)

	upstream := upstreamWithUsage(t)
	path := configSandbox(t, fmt.Sprintf(runnableConfig, "openai", upstream.URL)+fmt.Sprintf(recordsSection, hook.URL+recordsPath))

	address, out, done, cancel := startRun(t, path)
	defer cancel()

	request, err := http.NewRequest(http.MethodPost, "http://"+address+"/v1/messages",
		strings.NewReader(`{"model":"gpt-4o","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatalf("构造请求失败: %v\n%s", err, out)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer sk-live")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("转发请求失败: %v\n%s", err, out)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("转发请求的状态码是 %d", response.StatusCode)
	}
	// 探活也会走 OnRequestDone，但它不是模型调用，不该进记录。
	if health, err := http.Get("http://" + address + "/healthz"); err == nil {
		_ = health.Body.Close()
	}

	// 这一条不足一批，靠停机冲刷发出去。
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("退出时报错: %v\n%s", err, out)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("取消之后服务没有退出")
	}

	var got batch
	select {
	case got = <-received:
	case <-time.After(5 * time.Second):
		t.Fatal("摄入端点没有收到记录")
	}
	if got.Secret != "test-secret" {
		t.Errorf("x-callback-secret 是 %q，期望 test-secret", got.Secret)
	}
	if len(got.Records) != 1 {
		t.Fatalf("收到 %d 行，期望 1 行", len(got.Records))
	}
	row := got.Records[0]
	if row.RequestID == "" {
		t.Error("request_id 为空")
	}
	if row.Status != "success" {
		t.Errorf("status 是 %q，期望 success", row.Status)
	}
	if row.Model != "gpt-4o" || row.ServedModel != "gpt-4o" {
		t.Errorf("model/served_model 是 %q/%q", row.Model, row.ServedModel)
	}
	if row.Entry != "messages" {
		t.Errorf("entry 是 %q，期望 messages", row.Entry)
	}
	if row.Channel != "example" {
		t.Errorf("channel 是 %q，期望 example", row.Channel)
	}
	if row.Attempts != 1 || row.Retried {
		t.Errorf("attempts/retried 是 %d/%v", row.Attempts, row.Retried)
	}
	if row.HTTPStatus != 200 || row.UpstreamCode != "200" {
		t.Errorf("http_status/upstream_code 是 %d/%q", row.HTTPStatus, row.UpstreamCode)
	}
	if row.LatencyMS == nil {
		t.Error("latency_ms 为空")
	}
	if row.PromptTokens == nil || *row.PromptTokens != 10 {
		t.Errorf("prompt_tokens 是 %v，期望 10", row.PromptTokens)
	}
	// cost 段没配，不发 billing 数组；身份两列由密钥校验认出来。
	if strings.Contains(got.Raw, "billing") {
		t.Errorf("没配 cost 段时不该发 billing 数组: %s", got.Raw)
	}
	if row.UserID == nil || *row.UserID != 263 {
		t.Errorf("user_id 是 %v，期望 263: %s", row.UserID, got.Raw)
	}
	if row.APIKeyID == nil || *row.APIKeyID != 42 {
		t.Errorf("api_key_id 是 %v，期望 42: %s", row.APIKeyID, got.Raw)
	}
}

// costSection 是追加到配置后面的 cost 段。渠道名与 runnableConfig 里的一致。
const costSection = `
cost:
  channels:
    example:
      cost_input_per_million: 1
      cost_output_per_million: 2
`

// TestRunPushesBillingWhenCostConfigured 配了 Records 段与 cost 段之后，一条成功调用
// 在 records 之外还推一行 billing：金额按用量与 upsell 倍率算，两列倍率规则非空。
func TestRunPushesBillingWhenCostConfigured(t *testing.T) {
	t.Setenv("TOKENMP_RECORDS_SECRET", "test-secret")

	type billingRow struct {
		RequestID              string          `json:"request_id"`
		UserID                 *int64          `json:"user_id"`
		APIKeyID               *int64          `json:"api_key_id"`
		Model                  string          `json:"model"`
		PromptTokens           *int            `json:"prompt_tokens"`
		CostAmount             float64         `json:"cost_amount"`
		RevenueAmount          float64         `json:"revenue_amount"`
		CostMultiplier         float64         `json:"cost_multiplier"`
		RevenueMultiplier      float64         `json:"revenue_multiplier"`
		CostMultiplierRules    json.RawMessage `json:"cost_multiplier_rules"`
		RevenueMultiplierRules json.RawMessage `json:"revenue_multiplier_rules"`
		Currency               string          `json:"currency"`
		BillingMode            string          `json:"billing_mode"`
		Status                 string          `json:"status"`
	}
	type batch struct {
		Records []recordsRow `json:"records"`
		Billing []billingRow `json:"billing"`
	}
	received := make(chan batch, 4)
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == apiKeyPath {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"ok":true,"data":{"user_id":263,"api_key_id":42,`+
				`"model_whitelist":null,"rate_limit_per_min":60,"expires_at":null,`+
				`"pricing":{"plan_id":3,"pricing_mode":"markup","markup_multiplier":1.15,"prices":[]}}}`)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var got batch
		_ = json.Unmarshal(body, &got)
		received <- got
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(hook.Close)

	upstream := upstreamWithUsage(t)
	path := configSandbox(t, fmt.Sprintf(runnableConfig, "openai", upstream.URL)+
		fmt.Sprintf(recordsSection, hook.URL+recordsPath)+costSection)

	address, out, done, cancel := startRun(t, path)
	defer cancel()

	request, err := http.NewRequest(http.MethodPost, "http://"+address+"/v1/messages",
		strings.NewReader(`{"model":"gpt-4o","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatalf("构造请求失败: %v\n%s", err, out)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer sk-live")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("转发请求失败: %v\n%s", err, out)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("转发请求的状态码是 %d\n%s", response.StatusCode, out)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("退出时报错: %v\n%s", err, out)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("取消之后服务没有退出")
	}

	var got batch
	select {
	case got = <-received:
	case <-time.After(5 * time.Second):
		t.Fatal("摄入端点没有收到记录")
	}
	if len(got.Records) != 1 || len(got.Billing) != 1 {
		t.Fatalf("收到 records=%d billing=%d，期望各 1 行", len(got.Records), len(got.Billing))
	}
	bill := got.Billing[0]
	if bill.RequestID != got.Records[0].RequestID {
		t.Errorf("billing 的 request_id 是 %q，与 records 的 %q 不一致", bill.RequestID, got.Records[0].RequestID)
	}
	// 用量是 prompt 10、completion 5：成本 = (10×1 + 5×2)/1e6，收入再乘 1.15。
	if math.Abs(bill.CostAmount-20.0/1e6) > 1e-12 {
		t.Errorf("cost_amount 是 %v，期望 %v", bill.CostAmount, 20.0/1e6)
	}
	if math.Abs(bill.RevenueAmount-23.0/1e6) > 1e-12 {
		t.Errorf("revenue_amount 是 %v，期望 %v", bill.RevenueAmount, 23.0/1e6)
	}
	if bill.RevenueMultiplier != 1.15 || bill.BillingMode != "markup" {
		t.Errorf("revenue_multiplier/billing_mode 是 %v/%q", bill.RevenueMultiplier, bill.BillingMode)
	}
	if len(bill.CostMultiplierRules) == 0 || len(bill.RevenueMultiplierRules) == 0 {
		t.Errorf("两列倍率规则都要非空: %+v", bill)
	}
	if bill.Currency != "CNY" || bill.Status != "settled" {
		t.Errorf("currency/status 是 %q/%q", bill.Currency, bill.Status)
	}
	if bill.UserID == nil || *bill.UserID != 263 {
		t.Errorf("user_id 是 %v", bill.UserID)
	}
}

// TestRunRecordsEndpointDownStillForwards 摄入端点不通时转发不受影响，服务照常退出。
func TestRunRecordsEndpointDownStillForwards(t *testing.T) {
	t.Setenv("TOKENMP_RECORDS_SECRET", "test-secret")
	// 停机冲刷不必等真实的 10 秒；端点不通时冲刷本来就是放弃。
	previous := recordsFlushTimeout
	recordsFlushTimeout = 200 * time.Millisecond
	defer func() { recordsFlushTimeout = previous }()

	// 先起后关，拿到一个没有监听的地址。
	closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	closed.Close()

	upstream := upstreamWithUsage(t)
	// 校验端点单独起一个还活着的：这条用例考的是「摄入端点不通不影响转发」，不是
	// 「校验端点不通」——后者是 fail_closed 的事，另有断言。
	lookup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true,"data":{"user_id":1,"api_key_id":2,`+
			`"model_whitelist":null,"rate_limit_per_min":null,"expires_at":null,"pricing":{}}}`)
	}))
	t.Cleanup(lookup.Close)

	section := `
records:
  endpoint: "%s"
  auth_endpoint: "%s/api/internal/api-key"
  secret_env: "TOKENMP_RECORDS_SECRET"
  batch_size: 1
  flush_interval: 1s
`
	path := configSandbox(t, fmt.Sprintf(runnableConfig, "openai", upstream.URL)+fmt.Sprintf(section, closed.URL, lookup.URL))

	address, out, done, cancel := startRun(t, path)
	defer cancel()

	request, err := http.NewRequest(http.MethodPost, "http://"+address+"/v1/messages",
		strings.NewReader(`{"model":"gpt-4o","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatalf("构造请求失败: %v\n%s", err, out)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer sk-live")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("端点不通时转发失败: %v\n%s", err, out)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("端点不通时转发的状态码是 %d", response.StatusCode)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("退出时报错: %v\n%s", err, out)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("取消之后服务没有退出")
	}
}

// TestRunRecordsMissingSecret 密钥环境变量没设置时启动就报错，与渠道凭据同一规矩。
func TestRunRecordsMissingSecret(t *testing.T) {
	path := configSandbox(t, fmt.Sprintf(runnableConfig, "openai", "https://api.example.com")+
		fmt.Sprintf(recordsSection, "http://127.0.0.1:6789/api/internal/records"))

	out, err := run(t, "run", "--dry-run", "--config", path)
	if err == nil {
		t.Fatalf("密钥环境变量没设置时应当报错:\n%s", out)
	}
	if !strings.Contains(err.Error(), "TOKENMP_RECORDS_SECRET") {
		t.Errorf("报错里应当点出缺失的环境变量: %v", err)
	}
}

// TestRunRejectsBadKeyAndSkipsRecord 无效密钥被校验端点拒绝，且不写记录：不知道是
// 谁、也没有上游渠道，写进去只会污染全站视图的统计。
func TestRunRejectsBadKeyAndSkipsRecord(t *testing.T) {
	t.Setenv("TOKENMP_RECORDS_SECRET", "test-secret")

	received := make(chan string, 4)
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == apiKeyPath {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"error":"密钥不存在"}`)
			return
		}
		received <- r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(hook.Close)

	upstream := upstreamWithUsage(t)
	path := configSandbox(t, fmt.Sprintf(runnableConfig, "openai", upstream.URL)+fmt.Sprintf(recordsSection, hook.URL+recordsPath))

	address, out, done, cancel := startRun(t, path)
	defer cancel()

	request, err := http.NewRequest(http.MethodPost, "http://"+address+"/v1/messages",
		strings.NewReader(`{"model":"gpt-4o","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatalf("构造请求失败: %v\n%s", err, out)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer sk-bad")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("请求失败: %v\n%s", err, out)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("未知密钥应当回 401，实际 %d\n%s", response.StatusCode, out)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("退出时报错: %v\n%s", err, out)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("取消之后服务没有退出")
	}

	select {
	case path := <-received:
		t.Fatalf("被密钥拦下的请求不该写记录，却收到了 %s", path)
	case <-time.After(300 * time.Millisecond):
	}
}

// TestRunRejectsMissingKey auth 开启时不带密钥的请求回 401。
func TestRunRejectsMissingKey(t *testing.T) {
	t.Setenv("TOKENMP_RECORDS_SECRET", "test-secret")

	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(hook.Close)

	upstream := upstreamWithUsage(t)
	path := configSandbox(t, fmt.Sprintf(runnableConfig, "openai", upstream.URL)+fmt.Sprintf(recordsSection, hook.URL+recordsPath))

	address, out, done, cancel := startRun(t, path)
	defer cancel()

	response, err := http.Post("http://"+address+"/v1/messages", "application/json",
		strings.NewReader(`{"model":"gpt-4o","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatalf("请求失败: %v\n%s", err, out)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("不带密钥应当回 401，实际 %d\n%s", response.StatusCode, out)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("取消之后服务没有退出")
	}
}

// TestRunRecordsDryRunShowsEndpoint dry-run 只预检，启动信息里带出记录推送的地址。
func TestRunRecordsDryRunShowsEndpoint(t *testing.T) {
	t.Setenv("TOKENMP_RECORDS_SECRET", "test-secret")
	path := configSandbox(t, fmt.Sprintf(runnableConfig, "openai", "https://api.example.com")+
		fmt.Sprintf(recordsSection, "http://127.0.0.1:6789/api/internal/records"))

	out, err := run(t, "run", "--dry-run", "--config", path)
	if err != nil {
		t.Fatalf("run --dry-run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "记录推送") || !strings.Contains(out, "127.0.0.1:6789") {
		t.Errorf("启动信息里应当报出记录推送地址:\n%s", out)
	}
	if !strings.Contains(out, "密钥校验") || !strings.Contains(out, "127.0.0.1:6789/api/internal/api-key") {
		t.Errorf("启动信息里应当报出校验端点:\n%s", out)
	}
}

// recordsRow 是测试里读摄入请求用的行，字段只要断言关心的那几个。
type recordsRow struct {
	RequestID    string   `json:"request_id"`
	Status       string   `json:"status"`
	Model        string   `json:"model"`
	ServedModel  string   `json:"served_model"`
	Entry        string   `json:"entry"`
	Channel      string   `json:"channel"`
	ChannelType  string   `json:"channel_type"`
	HTTPStatus   int      `json:"http_status"`
	UpstreamCode string   `json:"upstream_code"`
	Attempts     int      `json:"attempts"`
	Retried      bool     `json:"retried"`
	LatencyMS    *int64   `json:"latency_ms"`
	PromptTokens *int     `json:"prompt_tokens"`
	TotalTokens  *int     `json:"total_tokens"`
	TPS          *float64 `json:"tps"`
	UserID       *int64   `json:"user_id"`
	APIKeyID     *int64   `json:"api_key_id"`
}

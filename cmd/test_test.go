package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// testConfig 是一份带一条路由规则的配置，渠道不鉴权，指向假上游。
const testConfig = `listen: 127.0.0.1:0

log:
  format: text
  level: info

channels:
  - name: fake
    dialect: %s
    base_url: %s
    auth:
      style: none
    timeout: 5s

models:
  - match: test-model
    channel: fake
`

// 四种上游方言的非流式响应样本，都带用量，内容固定好断言。
const (
	openAIChatUpstream = `{"id":"chatcmpl-1","object":"chat.completion","created":1,"model":"upstream-model",` +
		`"choices":[{"index":0,"message":{"role":"assistant","content":"来自假上游"},"finish_reason":"stop"}],` +
		`"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`
	claudeUpstream = `{"id":"msg_1","type":"message","role":"assistant","model":"upstream-model",` +
		`"content":[{"type":"text","text":"来自假上游"}],"stop_reason":"end_turn",` +
		`"usage":{"input_tokens":3,"output_tokens":2}}`
	responsesUpstream = `{"id":"resp_1","object":"response","created_at":1,"model":"upstream-model","status":"completed",` +
		`"output":[{"type":"message","id":"msg_1","role":"assistant","status":"completed",` +
		`"content":[{"type":"output_text","text":"来自假上游","annotations":[]}]}],` +
		`"usage":{"input_tokens":3,"output_tokens":2,"total_tokens":5}}`
	geminiUpstream = `{"candidates":[{"content":{"role":"model","parts":[{"text":"来自假上游"}]},"finishReason":"STOP"}],` +
		`"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":2,"totalTokenCount":5}}`
)

// openAIStreamUpstream 是 OpenAI Chat 的流式分片，结束在 [DONE]。
const openAIStreamUpstream = `data: {"id":"chatcmpl-1","object":"chat.completion.chunk","created":1,"model":"test-model","choices":[{"index":0,"delta":{"role":"assistant","content":"你"}}]}` + "\n\n" +
	`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","created":1,"model":"test-model","choices":[{"index":0,"delta":{"content":"好"},"finish_reason":"stop"}]}` + "\n\n" +
	"data: [DONE]\n\n"

// newTestUpstream 起一个假上游，固定回一份响应，并记下收到的请求体。
func newTestUpstream(t *testing.T, status int, contentType, response string) (*httptest.Server, *syncBuffer) {
	t.Helper()

	received := &syncBuffer{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_, _ = received.Write(body)
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(status)
		fmt.Fprint(w, response)
	}))
	t.Cleanup(upstream.Close)
	return upstream, received
}

func TestTestDefaultHitsUpstreamAndPrintsUsage(t *testing.T) {
	upstream, received := newTestUpstream(t, http.StatusOK, "application/json", claudeUpstream)
	path := configSandbox(t, fmt.Sprintf(testConfig, "claude", upstream.URL))

	out, err := run(t, "test", "--config", path, "--model", "test-model", "--prompt", "你好")
	if err != nil {
		t.Fatalf("test: %v\n%s", err, out)
	}
	for _, want := range []string{"候选路由", "请求结果", "用量", "prompt=", "回答", "来自假上游", "fake"} {
		if !strings.Contains(out, want) {
			t.Errorf("输出里没有 %q:\n%s", want, out)
		}
	}
	if received.String() == "" {
		t.Error("假上游没有收到请求")
	}
}

func TestTestEveryClientDialect(t *testing.T) {
	cases := []struct {
		client   string
		channel  string
		response string
	}{
		{"openai", "claude", claudeUpstream},
		{"responses", "claude", claudeUpstream},
		{"claude", "openai", openAIChatUpstream},
		{"gemini", "openai", openAIChatUpstream},
	}
	for _, tc := range cases {
		t.Run(tc.client, func(t *testing.T) {
			upstream, _ := newTestUpstream(t, http.StatusOK, "application/json", tc.response)
			path := configSandbox(t, fmt.Sprintf(testConfig, tc.channel, upstream.URL))

			out, err := run(t, "test", "--config", path, "--model", "test-model", "--dialect", tc.client)
			if err != nil {
				t.Fatalf("test --dialect %s: %v\n%s", tc.client, err, out)
			}
			for _, want := range []string{"请求结果", "状态", "200", "回答", "来自假上游"} {
				if !strings.Contains(out, want) {
					t.Errorf("%s 输出里没有 %q:\n%s", tc.client, want, out)
				}
			}
		})
	}
}

func TestTestStream(t *testing.T) {
	upstream, _ := newTestUpstream(t, http.StatusOK, "text/event-stream", openAIStreamUpstream)
	path := configSandbox(t, fmt.Sprintf(testConfig, "openai", upstream.URL))

	out, err := run(t, "test", "--config", path, "--model", "test-model", "--stream")
	if err != nil {
		t.Fatalf("test --stream: %v\n%s", err, out)
	}
	if !strings.Contains(out, "回答") || !strings.Contains(out, "你好") {
		t.Errorf("流式回答没有拼出来:\n%s", out)
	}
	if !strings.Contains(out, "TTFT") {
		t.Errorf("流式请求应当记下 TTFT:\n%s", out)
	}
}

func TestTestUpstreamError(t *testing.T) {
	upstream, _ := newTestUpstream(t, http.StatusInternalServerError, "application/json", `{"error":{"message":"上游炸了"}}`)
	path := configSandbox(t, fmt.Sprintf(testConfig, "claude", upstream.URL))

	out, err := run(t, "test", "--config", path, "--model", "test-model")
	if err == nil {
		t.Fatal("上游 500 时应当报错")
	}
	if !strings.Contains(out, "上游炸了") {
		t.Errorf("输出里没有上游错误体:\n%s", out)
	}
	if !strings.Contains(out, "500") {
		t.Errorf("输出里没有状态码:\n%s", out)
	}
}

func TestTestNoMatchingModel(t *testing.T) {
	upstream, _ := newTestUpstream(t, http.StatusOK, "application/json", claudeUpstream)
	path := configSandbox(t, fmt.Sprintf(testConfig, "claude", upstream.URL))

	out, err := run(t, "test", "--config", path, "--model", "absent-model")
	if err == nil {
		t.Fatal("模型没有匹配规则时应当报错")
	}
	if !strings.Contains(out, "没有匹配") {
		t.Errorf("输出里没有说明没有匹配规则:\n%s", out)
	}
}

func TestTestJSON(t *testing.T) {
	upstream, _ := newTestUpstream(t, http.StatusOK, "application/json", claudeUpstream)
	path := configSandbox(t, fmt.Sprintf(testConfig, "claude", upstream.URL))

	out, err := run(t, "test", "--config", path, "--model", "test-model", "--json")
	if err != nil {
		t.Fatalf("test --json: %v\n%s", err, out)
	}

	var payload struct {
		Model  string `json:"model"`
		Routes []struct {
			Channel  string `json:"channel"`
			Upstream string `json:"upstream"`
			Dialect  string `json:"dialect"`
		} `json:"routes"`
		Result *struct {
			Channel  string `json:"channel"`
			Upstream string `json:"upstream"`
			Status   int    `json:"status"`
			Usage    *struct {
				Prompt int `json:"prompt_tokens"`
			} `json:"usage"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("输出不是合法 JSON: %v\n%s", err, out)
	}
	if len(payload.Routes) != 1 {
		t.Fatalf("候选路由数是 %d，期望 1:\n%s", len(payload.Routes), out)
	}
	if payload.Result == nil {
		t.Fatal("JSON 里没有请求结果")
	}
	if payload.Result.Channel != "fake" || payload.Result.Upstream != "test-model" {
		t.Errorf("结果里的渠道与上游模型不对: %+v", payload.Result)
	}
	if payload.Result.Status != http.StatusOK {
		t.Errorf("状态码是 %d，期望 200", payload.Result.Status)
	}
	if payload.Result.Usage == nil || payload.Result.Usage.Prompt == 0 {
		t.Errorf("JSON 里没有用量: %+v", payload.Result.Usage)
	}
}

func TestTestRawPrintsResponseBody(t *testing.T) {
	upstream, _ := newTestUpstream(t, http.StatusOK, "application/json", claudeUpstream)
	path := configSandbox(t, fmt.Sprintf(testConfig, "claude", upstream.URL))

	out, err := run(t, "test", "--config", path, "--model", "test-model", "--raw")
	if err != nil {
		t.Fatalf("test --raw: %v\n%s", err, out)
	}
	if !strings.Contains(out, "响应体") || !strings.Contains(out, "choices") {
		t.Errorf("--raw 没有打印完整响应体:\n%s", out)
	}
}

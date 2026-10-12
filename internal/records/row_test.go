package records

import (
	"reflect"
	"testing"
	"time"

	"github.com/tokenmp/core/dialect"
	"github.com/tokenmp/core/server"
)

// testNow 是补时间戳用的固定时刻，避免用真实时钟让断言不稳定。
var testNow = time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)

func intPtr(value int) *int           { return &value }
func int64Ptr(value int64) *int64     { return &value }
func floatPtr(value float64) *float64 { return &value }

// TestFromOutcome 逐条核对字段映射。now 固定，时间戳因此可以直接算出来。
func TestFromOutcome(t *testing.T) {
	cases := []struct {
		name    string
		outcome server.Outcome
		want    Row
	}{
		{
			name: "流式成功，带用量与首字节",
			outcome: server.Outcome{
				RequestID: "req_stream",
				Path:      dialect.OpenAIChat.LocalPath(),
				Status:    200,
				Duration:  2 * time.Second,
				FirstByte: 800 * time.Millisecond,
				Model:     "gpt-4o",
				Channel:   "openai-main",
				Streaming: true,
				UserAgent: "curl/8.22.0",
				Attempts:  []server.Attempt{{Channel: "openai-main", Dialect: dialect.OpenAIChat, Upstream: "https://api.example.com/v1/chat/completions", Status: 200}},
				Usage:     &server.Usage{PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150, CachedTokens: 20, CacheWriteTokens: 10},
			},
			want: Row{
				RequestID:        "req_stream",
				Status:           "success",
				Model:            "gpt-4o",
				ServedModel:      "gpt-4o",
				Entry:            "chat",
				Stream:           true,
				Channel:          "openai-main",
				ChannelType:      string(dialect.OpenAIChat),
				BaseURL:          "https://api.example.com/v1/chat/completions",
				HTTPStatus:       200,
				UpstreamCode:     "200",
				Attempts:         1,
				Retried:          false,
				UserAgent:        "curl/8.22.0",
				LatencyMS:        int64Ptr(2000),
				TTFBMS:           int64Ptr(800),
				TPS:              floatPtr(41.7),
				PromptTokens:     intPtr(100),
				CompletionTokens: intPtr(50),
				CacheReadTokens:  intPtr(20),
				CacheWriteTokens: intPtr(10),
				TotalTokens:      intPtr(150),
				StartedAt:        testNow.Add(-2 * time.Second),
				FinishedAt:       testNow,
				CreatedAt:        testNow.Add(-2 * time.Second),
			},
		},
		{
			name: "失败请求，最后一次尝试拿到上游状态码",
			outcome: server.Outcome{
				RequestID: "req_fail",
				Path:      dialect.ClaudeMessages.LocalPath(),
				Status:    502,
				Duration:  3 * time.Second,
				Model:     "claude-3",
				Attempts: []server.Attempt{
					{Channel: "openai-main", Dialect: dialect.OpenAIChat, Upstream: "https://a.example.com/v1/chat/completions", Status: 0, Failure: server.FailureConnect},
					{Channel: "claude-main", Dialect: dialect.ClaudeMessages, Upstream: "https://b.example.com/v1/messages", Status: 502},
				},
			},
			want: Row{
				RequestID:    "req_fail",
				Status:       "error",
				Model:        "claude-3",
				ServedModel:  "claude-3",
				Entry:        "messages",
				ChannelType:  string(dialect.ClaudeMessages),
				BaseURL:      "https://b.example.com/v1/messages",
				HTTPStatus:   502,
				UpstreamCode: "502",
				Attempts:     2,
				Retried:      true,
				LatencyMS:    int64Ptr(3000),
				StartedAt:    testNow.Add(-3 * time.Second),
				FinishedAt:   testNow,
				CreatedAt:    testNow.Add(-3 * time.Second),
			},
		},
		{
			name: "最后一次尝试没连上上游",
			outcome: server.Outcome{
				RequestID: "req_connect",
				Path:      dialect.OpenAIResponses.LocalPath(),
				Status:    502,
				Duration:  500 * time.Millisecond,
				Model:     "gpt-4o",
				Attempts: []server.Attempt{
					{Channel: "openai-main", Dialect: dialect.OpenAIResponses, Upstream: "https://c.example.com/v1/responses", Status: 0, Failure: server.FailureConnect},
				},
			},
			want: Row{
				RequestID:   "req_connect",
				Status:      "error",
				Model:       "gpt-4o",
				ServedModel: "gpt-4o",
				Entry:       "responses",
				ChannelType: string(dialect.OpenAIResponses),
				BaseURL:     "https://c.example.com/v1/responses",
				HTTPStatus:  502,
				ErrorCode:   string(server.FailureConnect),
				Attempts:    1,
				LatencyMS:   int64Ptr(500),
				StartedAt:   testNow.Add(-500 * time.Millisecond),
				FinishedAt:  testNow,
				CreatedAt:   testNow.Add(-500 * time.Millisecond),
			},
		},
		{
			name: "Gemini 非流式，无用量无尝试",
			outcome: server.Outcome{
				RequestID: "req_gemini",
				Path:      "/v1beta/models/gemini-2.5-pro:generateContent",
				Status:    200,
				Duration:  120 * time.Millisecond,
				Model:     "gemini-2.5-pro",
			},
			want: Row{
				RequestID:   "req_gemini",
				Status:      "success",
				Model:       "gemini-2.5-pro",
				ServedModel: "gemini-2.5-pro",
				Entry:       "gemini",
				HTTPStatus:  200,
				LatencyMS:   int64Ptr(120),
				StartedAt:   testNow.Add(-120 * time.Millisecond),
				FinishedAt:  testNow,
				CreatedAt:   testNow.Add(-120 * time.Millisecond),
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := FromOutcome(tc.outcome, testNow)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("FromOutcome 与期望不一致\n--- got ---\n%+v\n--- want ---\n%+v", got, tc.want)
			}
		})
	}
}

// TestFromOutcomeLeavesIdentityEmpty 守住两处现在拿不到的值：身份两列恒为空，计费
// 数组不发。等 web 侧给出密钥校验、金额口径定下来之后再改，这条测试要跟着改。
func TestFromOutcomeLeavesIdentityEmpty(t *testing.T) {
	row := FromOutcome(server.Outcome{RequestID: "req_1", Status: 200}, testNow)
	if row.UserID != nil || row.APIKeyID != nil {
		t.Errorf("身份两列现在应当留空，实际 user_id=%v api_key_id=%v", row.UserID, row.APIKeyID)
	}
	if row.ErrorMessage != "" || row.UpstreamMessage != "" || row.PanelMessage != "" {
		t.Errorf("错误文字现在应当留空，实际 %q %q %q", row.ErrorMessage, row.UpstreamMessage, row.PanelMessage)
	}
}

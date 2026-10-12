package clientauth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tokenmp/core/dialect"
)

// TestExtractKey 覆盖各家客户端放密钥的位置。
func TestExtractKey(t *testing.T) {
	cases := []struct {
		name   string
		target string
		header map[string]string
		dial   dialect.Name
		want   string
		ok     bool
	}{
		{
			name:   "OpenAI 的 Authorization Bearer",
			target: "/v1/chat/completions",
			header: map[string]string{"Authorization": "Bearer sk-abc"},
			dial:   dialect.OpenAIChat,
			want:   "sk-abc",
			ok:     true,
		},
		{
			name:   "Bearer 方案名大小写不计",
			target: "/v1/chat/completions",
			header: map[string]string{"Authorization": "bearer sk-abc"},
			dial:   dialect.OpenAIChat,
			want:   "sk-abc",
			ok:     true,
		},
		{
			name:   "Claude 的 x-api-key",
			target: "/v1/messages",
			header: map[string]string{"x-api-key": "sk-abc"},
			dial:   dialect.ClaudeMessages,
			want:   "sk-abc",
			ok:     true,
		},
		{
			name:   "Responses 的 x-api-key",
			target: "/v1/responses",
			header: map[string]string{"x-api-key": "sk-abc"},
			dial:   dialect.OpenAIResponses,
			want:   "sk-abc",
			ok:     true,
		},
		{
			name:   "Gemini 的 x-goog-api-key",
			target: "/v1beta/models/gemini-2.0:generateContent",
			header: map[string]string{"x-goog-api-key": "sk-abc"},
			dial:   dialect.GeminiGenerate,
			want:   "sk-abc",
			ok:     true,
		},
		{
			name:   "Gemini 查询串里的 key",
			target: "/v1beta/models/gemini-2.0:generateContent?key=sk-abc",
			dial:   dialect.GeminiGenerate,
			want:   "sk-abc",
			ok:     true,
		},
		{
			name:   "没有密钥",
			target: "/v1/chat/completions",
			dial:   dialect.OpenAIChat,
		},
		{
			name:   "Authorization 不是 Bearer",
			target: "/v1/chat/completions",
			header: map[string]string{"Authorization": "Basic abc"},
			dial:   dialect.OpenAIChat,
		},
		{
			name:   "Bearer 之后为空",
			target: "/v1/chat/completions",
			header: map[string]string{"Authorization": "Bearer   "},
			dial:   dialect.OpenAIChat,
		},
		{
			name:   "Gemini 不认 Authorization Bearer",
			target: "/v1beta/models/gemini-2.0:generateContent",
			header: map[string]string{"Authorization": "Bearer sk-abc"},
			dial:   dialect.GeminiGenerate,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, tc.target, nil)
			for name, value := range tc.header {
				request.Header.Set(name, value)
			}
			got, ok := extractKey(tc.dial, request)
			if ok != tc.ok {
				t.Fatalf("取到密钥的判定是 %v，期望 %v", ok, tc.ok)
			}
			if got != tc.want {
				t.Errorf("取到的密钥是 %q，期望 %q", got, tc.want)
			}
		})
	}
}

// TestDialectOfPath 只有四种模型端点要过校验，其余交给 core。
func TestDialectOfPath(t *testing.T) {
	cases := []struct {
		path string
		want dialect.Name
		ok   bool
	}{
		{"/v1/chat/completions", dialect.OpenAIChat, true},
		{"/v1/responses", dialect.OpenAIResponses, true},
		{"/v1/messages", dialect.ClaudeMessages, true},
		{"/v1beta/models/gemini-2.0:generateContent", dialect.GeminiGenerate, true},
		{"/healthz", "", false},
		{"/v1/models", "", false},
		{"/nope", "", false},
	}

	for _, tc := range cases {
		got, ok := dialectOfPath(tc.path)
		if got != tc.want || ok != tc.ok {
			t.Errorf("路径 %s 判成 %q/%v，期望 %q/%v", tc.path, got, ok, tc.want, tc.ok)
		}
	}
}

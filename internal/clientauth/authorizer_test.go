package clientauth

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tokenmp/core/dialect"
)

// lookupStub 是一个假的密钥校验端点，记录收到的密钥与请求头。
type lookupStub struct {
	server *httptest.Server
	calls  atomic.Int64
	// respond 按密钥给出状态码与响应体。
	respond func(key string) (int, string)

	mu     sync.Mutex
	keys   []string
	secret string
}

func newLookupStub(t *testing.T, respond func(key string) (int, string)) *lookupStub {
	t.Helper()
	stub := &lookupStub{respond: respond}
	stub.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body struct {
			Key string `json:"key"`
		}
		_ = json.NewDecoder(request.Body).Decode(&body)
		stub.calls.Add(1)
		stub.mu.Lock()
		stub.keys = append(stub.keys, body.Key)
		stub.secret = request.Header.Get("x-callback-secret")
		stub.mu.Unlock()
		status, payload := stub.respond(body.Key)
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(status)
		_, _ = io.WriteString(writer, payload)
	}))
	t.Cleanup(stub.server.Close)
	return stub
}

// logBuffer 是并发安全的日志缓冲。
type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *logBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// newTestLogger 返回一个把日志收进缓冲的 logger。
func newTestLogger() (*slog.Logger, *logBuffer) {
	buffer := &logBuffer{}
	return slog.New(slog.NewTextHandler(buffer, &slog.HandlerOptions{Level: slog.LevelDebug})), buffer
}

// okPayload 是一把有效密钥的校验响应。
const okPayload = `{"ok":true,"data":{"user_id":263,"api_key_id":42,` +
	`"model_whitelist":null,"rate_limit_per_min":60,"expires_at":null,` +
	`"pricing":{"plan_id":3,"pricing_mode":"markup","markup_multiplier":1.15,"prices":[]}}}`

// newFront 用给定的校验端点和下游处理器起一个前端服务。
func newFront(t *testing.T, endpoint string, failClosed bool, next http.Handler) (*httptest.Server, *logBuffer) {
	t.Helper()
	logger, buffer := newTestLogger()
	authorizer := New(Options{
		Endpoint:   endpoint,
		Secret:     "test-secret",
		CacheTTL:   time.Minute,
		FailClosed: failClosed,
	}, logger)
	if next == nil {
		next = http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(http.StatusOK)
			_, _ = writer.Write([]byte("forwarded"))
		})
	}
	front := httptest.NewServer(authorizer.Middleware(next))
	t.Cleanup(front.Close)
	return front, buffer
}

// postJSON 往前端发一条 OpenAI Chat 请求。
func postJSON(t *testing.T, url, body, authorization string) (*http.Response, string) {
	t.Helper()
	return postJSONPath(t, url, dialect.OpenAIChat.LocalPath(), body, authorization)
}

// postJSONPath 往前端发一条指定路径的 JSON 请求。计数端点与 Messages 同形，
// 走的也是同一条中间件分支。
func postJSONPath(t *testing.T, url, path, body, authorization string) (*http.Response, string) {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, url+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	if authorization != "" {
		request.Header.Set("Authorization", authorization)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	payload, _ := io.ReadAll(response.Body)
	return response, string(payload)
}

// TestMiddlewareValidKeyReachesNextAndCarriesIdentity 有效密钥放行，身份进 context。
func TestMiddlewareValidKeyReachesNextAndCarriesIdentity(t *testing.T) {
	stub := newLookupStub(t, func(string) (int, string) { return http.StatusOK, okPayload })
	var (
		called   bool
		identity Identity
	)
	next := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		called = true
		identity, _ = FromContext(request.Context())
		writer.WriteHeader(http.StatusOK)
	})
	front, _ := newFront(t, stub.server.URL, true, next)

	response, body := postJSON(t, front.URL, `{"model":"gpt-4o","messages":[]}`, "Bearer sk-live")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("状态码是 %d: %s", response.StatusCode, body)
	}
	if !called {
		t.Fatal("有效密钥应当放行到下游")
	}
	if identity.UserID != 263 || identity.APIKeyID != 42 {
		t.Errorf("身份是 %+v，期望 263/42", identity)
	}
	if identity.Pricing.Mode != PricingModeMarkup || identity.Pricing.MarkupMultiplier != 1.15 {
		t.Errorf("计价口径是 %+v，期望 markup/1.15", identity.Pricing)
	}
	stub.mu.Lock()
	defer stub.mu.Unlock()
	if stub.secret != "test-secret" {
		t.Errorf("x-callback-secret 是 %q，期望 test-secret", stub.secret)
	}
	if len(stub.keys) != 1 || stub.keys[0] != "sk-live" {
		t.Errorf("端点收到的密钥是 %v，期望 [sk-live]", stub.keys)
	}
}

// TestMiddlewareRejections 未知、停用、过期的密钥按校验端点给的状态码拒绝，且不调 next。
func TestMiddlewareRejections(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		payload    string
		wantStatus int
		wantCode   string
		// wantReason 是客户端能看到的 message。三类失败状态码与错误码都一样，
		// 差别只在这里，客户端据此才能自己看出是不是过期了。
		wantReason string
	}{
		{
			name:       "未知密钥",
			status:     http.StatusNotFound,
			payload:    `{"error":"密钥不存在"}`,
			wantStatus: http.StatusUnauthorized,
			wantCode:   "invalid_api_key",
			wantReason: "密钥不存在",
		},
		{
			name:       "密钥或账号停用",
			status:     http.StatusForbidden,
			payload:    `{"error":"密钥已停用"}`,
			wantStatus: http.StatusUnauthorized,
			wantCode:   "invalid_api_key",
			wantReason: "密钥或账号已停用",
		},
		{
			name:       "密钥过期",
			status:     http.StatusGone,
			payload:    `{"error":"密钥已过期"}`,
			wantStatus: http.StatusUnauthorized,
			wantCode:   "invalid_api_key",
			wantReason: "密钥已过期",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := newLookupStub(t, func(string) (int, string) { return tc.status, tc.payload })
			called := false
			next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })
			front, _ := newFront(t, stub.server.URL, true, next)

			response, body := postJSON(t, front.URL, `{"model":"gpt-4o","messages":[]}`, "Bearer sk-live")
			if response.StatusCode != tc.wantStatus {
				t.Fatalf("状态码是 %d，期望 %d: %s", response.StatusCode, tc.wantStatus, body)
			}
			if !strings.Contains(body, tc.wantCode) {
				t.Errorf("错误体里没有 %q: %s", tc.wantCode, body)
			}
			if !strings.Contains(body, tc.wantReason) {
				t.Errorf("错误体里看不出是哪一类失败，缺 %q: %s", tc.wantReason, body)
			}
			if called {
				t.Error("被拒的请求不该调 next")
			}
		})
	}
}

// TestMiddlewareMissingKey 没带密钥时回 401，不调校验端点。
func TestMiddlewareMissingKey(t *testing.T) {
	stub := newLookupStub(t, func(string) (int, string) { return http.StatusOK, okPayload })
	called := false
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })
	front, _ := newFront(t, stub.server.URL, true, next)

	response, body := postJSON(t, front.URL, `{"model":"gpt-4o","messages":[]}`, "")
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("状态码是 %d，期望 401: %s", response.StatusCode, body)
	}
	if called {
		t.Error("没带密钥的请求不该调 next")
	}
	if got := stub.calls.Load(); got != 0 {
		t.Errorf("没带密钥不该打校验端点，实际打了 %d 次", got)
	}
}

// postCountTokens 往前端发一条 Claude Messages 计数端点的请求。
func postCountTokens(t *testing.T, url, body, authorization string) (*http.Response, string) {
	t.Helper()
	return postJSONPath(t, url, dialect.ClaudeCountTokensPath, body, authorization)
}

// TestMiddlewareCountTokensGuards 计数端点在 HTTP 层要跟 /v1/messages 一个待遇。
//
// 这条打的是一整条中间件，不是 dialectOfPath 那张表：路径的归属判对了不等于中间件
// 用它判了。漏掉它的产物是计数端点从不带密钥的请求也放行 —— 网关配的渠道凭据
// 就白送给上游了，因此四种行为都在这里钉住。
func TestMiddlewareCountTokensGuards(t *testing.T) {
	const body = `{"model":"claude-x","messages":[{"role":"user","content":"hi"}]}`

	t.Run("没带密钥回 401，不调 next、不打校验端点", func(t *testing.T) {
		stub := newLookupStub(t, func(string) (int, string) { return http.StatusOK, okPayload })
		called := false
		next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })
		front, _ := newFront(t, stub.server.URL, true, next)

		response, payload := postCountTokens(t, front.URL, body, "")
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("状态码是 %d，期望 401: %s", response.StatusCode, payload)
		}
		if !strings.Contains(payload, "invalid_api_key") {
			t.Errorf("错误体形状应当与 /v1/messages 一致: %s", payload)
		}
		if called {
			t.Error("没带密钥的请求不该调 next")
		}
		if got := stub.calls.Load(); got != 0 {
			t.Errorf("没带密钥不该打校验端点，实际打了 %d 次", got)
		}
	})

	t.Run("带正确密钥放行，身份进 context", func(t *testing.T) {
		stub := newLookupStub(t, func(string) (int, string) { return http.StatusOK, okPayload })
		var (
			called   bool
			identity Identity
		)
		next := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			called = true
			identity, _ = FromContext(request.Context())
			writer.WriteHeader(http.StatusOK)
		})
		front, _ := newFront(t, stub.server.URL, true, next)

		response, payload := postCountTokens(t, front.URL, body, "Bearer sk-live")
		if response.StatusCode != http.StatusOK {
			t.Fatalf("状态码是 %d，期望 200: %s", response.StatusCode, payload)
		}
		if !called {
			t.Fatal("有效密钥应当放行到下游")
		}
		if identity.UserID != 263 || identity.APIKeyID != 42 {
			t.Errorf("身份是 %+v，期望 263/42", identity)
		}
	})

	t.Run("端点不通且 fail_closed 时回 503", func(t *testing.T) {
		closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		closed.Close()

		front, _ := newFront(t, closed.URL, true, nil)
		response, payload := postCountTokens(t, front.URL, body, "Bearer sk-live")
		if response.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("状态码是 %d，期望 503: %s", response.StatusCode, payload)
		}
		if !strings.Contains(payload, "identity_unavailable") {
			t.Errorf("错误体里应当点出身份校验不可用: %s", payload)
		}
	})

	t.Run("模型不在白名单时回 403", func(t *testing.T) {
		stub := newLookupStub(t, func(string) (int, string) {
			return http.StatusOK, `{"ok":true,"data":{"user_id":1,"api_key_id":2,` +
				`"model_whitelist":["claude-other"],"rate_limit_per_min":null,"expires_at":null,"pricing":{}}}`
		})
		called := false
		next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })
		front, _ := newFront(t, stub.server.URL, true, next)

		// 计数端点的模型从请求体里读，与 Messages 同一处口径。
		response, payload := postCountTokens(t, front.URL, body, "Bearer sk-live")
		if response.StatusCode != http.StatusForbidden {
			t.Fatalf("状态码是 %d，期望 403: %s", response.StatusCode, payload)
		}
		if called {
			t.Error("名单外的请求不该调 next")
		}
	})
}

// TestMiddlewareWhitelist 模型白名单生效：名单为空不限制，名单内的放行，名单外的拒绝。
func TestMiddlewareWhitelist(t *testing.T) {
	payload := func(whitelist string) string {
		return `{"ok":true,"data":{"user_id":1,"api_key_id":2,"model_whitelist":` + whitelist +
			`,"rate_limit_per_min":null,"expires_at":null,"pricing":{}}}`
	}
	cases := []struct {
		name       string
		whitelist  string
		model      string
		wantStatus int
	}{
		{name: "名单为空不限制", whitelist: "null", model: "gpt-4o", wantStatus: http.StatusOK},
		{name: "名单内放行", whitelist: `["gpt-4o"]`, model: "gpt-4o", wantStatus: http.StatusOK},
		{name: "名单外拒绝", whitelist: `["gpt-4o"]`, model: "gpt-3.5", wantStatus: http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := newLookupStub(t, func(string) (int, string) { return http.StatusOK, payload(tc.whitelist) })
			front, _ := newFront(t, stub.server.URL, true, nil)
			response, body := postJSON(t, front.URL,
				`{"model":"`+tc.model+`","messages":[]}`, "Bearer sk-live")
			if response.StatusCode != tc.wantStatus {
				t.Fatalf("状态码是 %d，期望 %d: %s", response.StatusCode, tc.wantStatus, body)
			}
		})
	}
}

// TestMiddlewareWhitelistGemini 白名单按路径里的模型名判，不读请求体。
func TestMiddlewareWhitelistGemini(t *testing.T) {
	stub := newLookupStub(t, func(string) (int, string) {
		return http.StatusOK, `{"ok":true,"data":{"user_id":1,"api_key_id":2,` +
			`"model_whitelist":["gemini-2.0"],"rate_limit_per_min":null,"expires_at":null,"pricing":{}}}`
	})
	front, _ := newFront(t, stub.server.URL, true, nil)
	response, _ := doGemini(t, front.URL+"/v1beta/models/gemini-2.0:generateContent", "sk-live")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("名单内的 Gemini 模型应当放行，状态码 %d", response.StatusCode)
	}
	response, _ = doGemini(t, front.URL+"/v1beta/models/gemini-1.5:generateContent", "sk-live")
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("名单外的 Gemini 模型应当拒绝，状态码 %d", response.StatusCode)
	}
}

// doGemini 发一条 Gemini 请求，密钥放 x-goog-api-key。
func doGemini(t *testing.T, url, key string) (*http.Response, string) {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, url, strings.NewReader(`{"contents":[]}`))
	if err != nil {
		t.Fatalf("构造 Gemini 请求失败: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("x-goog-api-key", key)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("Gemini 请求失败: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	payload, _ := io.ReadAll(response.Body)
	return response, string(payload)
}

// TestMiddlewareCachesLookup 同一把密钥连续请求只打一次校验端点；换一把密钥要重新打。
func TestMiddlewareCachesLookup(t *testing.T) {
	stub := newLookupStub(t, func(string) (int, string) { return http.StatusOK, okPayload })
	front, _ := newFront(t, stub.server.URL, true, nil)

	for i := 0; i < 3; i++ {
		response, body := postJSON(t, front.URL, `{"model":"gpt-4o","messages":[]}`, "Bearer sk-same")
		if response.StatusCode != http.StatusOK {
			t.Fatalf("第 %d 次请求状态码是 %d: %s", i+1, response.StatusCode, body)
		}
	}
	if got := stub.calls.Load(); got != 1 {
		t.Fatalf("同一把密钥应当只打一次校验端点，实际 %d 次", got)
	}

	if _, body := postJSON(t, front.URL, `{"model":"gpt-4o","messages":[]}`, "Bearer sk-other"); body == "" {
		t.Fatal("换密钥的请求没有响应")
	}
	if got := stub.calls.Load(); got != 2 {
		t.Fatalf("换一把密钥应当再打一次校验端点，实际 %d 次", got)
	}
}

// TestMiddlewareCachesRejection 被拒的结果同样缓存，无效密钥连续打不会每次都问端点。
func TestMiddlewareCachesRejection(t *testing.T) {
	stub := newLookupStub(t, func(string) (int, string) {
		return http.StatusNotFound, `{"error":"密钥不存在"}`
	})
	front, _ := newFront(t, stub.server.URL, true, nil)
	for i := 0; i < 3; i++ {
		response, _ := postJSON(t, front.URL, `{"model":"gpt-4o","messages":[]}`, "Bearer sk-bad")
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("第 %d 次请求状态码是 %d，期望 401", i+1, response.StatusCode)
		}
	}
	if got := stub.calls.Load(); got != 1 {
		t.Fatalf("被拒的结果也该缓存，实际打了 %d 次端点", got)
	}
}

// TestMiddlewareUnreachableFailClosed 端点不通时按配置拒绝，日志里带计数。
func TestMiddlewareUnreachableFailClosed(t *testing.T) {
	// 先起后关，拿到一个没有监听的地址。
	closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	closed.Close()

	front, logs := newFront(t, closed.URL, true, nil)
	response, body := postJSON(t, front.URL, `{"model":"gpt-4o","messages":[]}`, "Bearer sk-live")
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("端点不通时应当拒绝，状态码是 %d: %s", response.StatusCode, body)
	}
	if !strings.Contains(body, "identity_unavailable") {
		t.Errorf("错误体里应当点出身份校验不可用: %s", body)
	}
	if !strings.Contains(logs.String(), "unreachable_total") {
		t.Errorf("端点不通要记带计数的 WARN: %s", logs.String())
	}
}

// TestMiddlewareUnreachableFailOpen 放开时端点不通也放行，但本次不带身份。
func TestMiddlewareUnreachableFailOpen(t *testing.T) {
	closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	closed.Close()

	var (
		called   bool
		hasIdent bool
	)
	next := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		called = true
		_, hasIdent = FromContext(request.Context())
		writer.WriteHeader(http.StatusOK)
	})
	front, logs := newFront(t, closed.URL, false, next)
	response, body := postJSON(t, front.URL, `{"model":"gpt-4o","messages":[]}`, "Bearer sk-live")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("放开时应当放行，状态码是 %d: %s", response.StatusCode, body)
	}
	if !called {
		t.Fatal("放开时应当调 next")
	}
	if hasIdent {
		t.Error("端点不通时放行的请求不该带身份")
	}
	if !strings.Contains(logs.String(), "放行请求") {
		t.Errorf("放开放行也要记一条 WARN: %s", logs.String())
	}
}

// TestMiddlewareSkipsNonModelPaths /healthz 与 /v1/models 不过校验。
func TestMiddlewareSkipsNonModelPaths(t *testing.T) {
	stub := newLookupStub(t, func(string) (int, string) { return http.StatusOK, okPayload })
	front, _ := newFront(t, stub.server.URL, true, nil)

	response, err := http.Get(front.URL + "/healthz")
	if err != nil {
		t.Fatalf("请求 /healthz 失败: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("/healthz 的状态码是 %d", response.StatusCode)
	}
	if got := stub.calls.Load(); got != 0 {
		t.Errorf("探活不该打校验端点，实际 %d 次", got)
	}
}

// TestFingerprintDoesNotLeakPlaintext 日志里只出现短哈希。
func TestFingerprintDoesNotLeakPlaintext(t *testing.T) {
	got := fingerprint("sk-very-secret")
	if got == "" || strings.Contains(got, "sk-very-secret") {
		t.Fatalf("指纹不该含明文: %q", got)
	}
	if len(got) != 16 {
		t.Errorf("指纹长度是 %d，期望 16", len(got))
	}
}

// TestFromContextWithoutIdentity 没装中间件时取不到身份。
func TestFromContextWithoutIdentity(t *testing.T) {
	if _, ok := FromContext(context.Background()); ok {
		t.Error("空 context 里不该有身份")
	}
}

// TestDecodeSuccessCarriesPricing table 模式的价目表要跟着身份一起带出来，供算金额用。
func TestDecodeSuccessCarriesPricing(t *testing.T) {
	payload := `{"ok":true,"data":{"user_id":1,"api_key_id":2,"model_whitelist":null,` +
		`"rate_limit_per_min":null,"expires_at":null,"pricing":{"plan_id":7,"pricing_mode":"table",` +
		`"markup_multiplier":1,"prices":[{"model":"gpt-4o","cost_mode":"token",` +
		`"input_per_million":3,"output_per_million":6,"cache_read_per_million":0.3,` +
		`"cache_write_per_million":1.5,"per_request":0,"request_weight":1}]}}}`
	result, err := decodeSuccess([]byte(payload))
	if err != nil {
		t.Fatalf("decodeSuccess: %v", err)
	}
	pricing := result.identity.Pricing
	if pricing.Mode != PricingModeTable {
		t.Fatalf("模式是 %q，期望 table", pricing.Mode)
	}
	if pricing.PlanID == nil || *pricing.PlanID != 7 {
		t.Errorf("plan_id 是 %v，期望 7", pricing.PlanID)
	}
	price, ok := pricing.Price("gpt-4o")
	if !ok {
		t.Fatalf("价目表里没有 gpt-4o: %+v", pricing.Prices)
	}
	if price.InputPerMillion != 3 || price.CacheWritePerMillion != 1.5 {
		t.Errorf("单价是 %+v", price)
	}
}

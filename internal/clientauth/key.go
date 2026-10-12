package clientauth

import (
	"bytes"
	"io"
	"net/http"
	"strings"

	"github.com/tokenmp/core/dialect"
)

// 客户端放密钥的请求头与查询参数。形状照各家客户端的实际写法，不另猜一套。
const (
	// keyHeader 是 Anthropic 与部分 OpenAI 客户端用的头。
	keyHeader = "x-api-key"
	// geminiKeyHeader 是 Gemini 官方的头。
	geminiKeyHeader = "x-goog-api-key"
	// geminiKeyQuery 是 Gemini 官方允许写在查询串里的写法。
	geminiKeyQuery = "key"
	// bearerPrefix 是 Authorization 头里的方案名，大小写不计。
	bearerPrefix = "Bearer "
)

// extractKey 按方言从请求里取出客户端密钥。
//
// OpenAI 与 Claude 认 Authorization: Bearer 与 x-api-key；Gemini 认 x-goog-api-key
// 与查询串里的 key。取不到时返回假值，由调用方决定回什么。
func extractKey(name dialect.Name, request *http.Request) (string, bool) {
	if name == dialect.GeminiGenerate {
		if key := strings.TrimSpace(request.Header.Get(geminiKeyHeader)); key != "" {
			return key, true
		}
		if key := strings.TrimSpace(request.URL.Query().Get(geminiKeyQuery)); key != "" {
			return key, true
		}
		return "", false
	}
	if key, ok := bearerToken(request.Header.Get("Authorization")); ok {
		return key, true
	}
	if key := strings.TrimSpace(request.Header.Get(keyHeader)); key != "" {
		return key, true
	}
	return "", false
}

// bearerToken 从 Authorization 头的值里取出 Bearer 之后的密钥。方案名大小写不计，
// 前缀之后为空时算没带。
func bearerToken(value string) (string, bool) {
	if len(value) < len(bearerPrefix) || !strings.EqualFold(value[:len(bearerPrefix)], bearerPrefix) {
		return "", false
	}
	token := strings.TrimSpace(value[len(bearerPrefix):])
	if token == "" {
		return "", false
	}
	return token, true
}

// modelOfRequest 取出请求要用的模型名，取不到时返回空串。
//
// Gemini 的模型名在路径上；其余三方言在请求体里，读之前把请求体缓存下来再放回去，
// core 后面还要从 request.Body 读一遍。只有密钥带了模型白名单时才会调到这里，因此
// 没有白名单的请求不会为它多留一份请求体。
func modelOfRequest(name dialect.Name, request *http.Request) string {
	if name == dialect.GeminiGenerate {
		return geminiModelOf(request.URL.Path)
	}
	body, err := readBody(request)
	if err != nil {
		return ""
	}
	model, err := dialect.ModelOf(name, body)
	if err != nil {
		return ""
	}
	return model
}

// geminiModelOf 从 /v1beta/models/{模型名}:{动作} 里取出模型名。取不到时返回空串。
func geminiModelOf(path string) string {
	rest := strings.TrimPrefix(path, geminiPrefix)
	actionIndex := strings.LastIndex(rest, ":")
	if actionIndex <= 0 {
		return ""
	}
	return rest[:actionIndex]
}

// readBody 把请求体读出来再原样放回去，返回读到的字节。
func readBody(request *http.Request) ([]byte, error) {
	if request.Body == nil {
		return nil, nil
	}
	body, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, err
	}
	_ = request.Body.Close()
	request.Body = io.NopCloser(bytes.NewReader(body))
	request.ContentLength = int64(len(body))
	return body, nil
}

// Package clientauth 在转发路径上认出调用方是谁：按方言从请求里取出客户端密钥，
// 调 web 的校验端点换成身份，把结果按密钥的哈希缓存起来。
//
// 它做成 core 的宿主中间件：核验通过就把身份挂进 context，转发继续；核验不通过就
// 自己写出错误响应，不调 next。不调 next 有两个后果，都是有意为之：core 不写访问
// 日志、也不触发 OnRequestDone，因此被密钥拦下的请求不会进 request_records —— 这类
// 请求没有 user_id，也没有上游渠道，写进去只会污染全站视图的统计。
package clientauth

import (
	"context"
	"strings"

	"github.com/tokenmp/core/dialect"
)

// Identity 是一次调用认出的身份，取自 web 校验端点的 user_id 与 api_key_id。
type Identity struct {
	UserID   int64
	APIKeyID int64
}

// identityKey 是身份在 context 里的键。用私有类型避免与别的包撞键。
type identityKey struct{}

// WithIdentity 把身份放进 context。
func WithIdentity(ctx context.Context, identity Identity) context.Context {
	return context.WithValue(ctx, identityKey{}, identity)
}

// FromContext 取出本次请求的身份。没经过校验中间件时返回假值。
func FromContext(ctx context.Context) (Identity, bool) {
	identity, ok := ctx.Value(identityKey{}).(Identity)
	return identity, ok
}

// 端点上与协议无关的固定路径。core 的 server 没有导出 Gemini 前缀，这里照 1 写一份，
// core 改路由时这里要跟着改（cmd/run.go 的启动信息也另写了一份）。
const geminiPrefix = "/v1beta/models/"

// dialectOfPath 按请求路径判断这是哪种方言的模型端点。不是模型端点时返回假值，
// 校验中间件由此放行 /healthz、/v1/models 与未知路径，交给 core 自己处理。
func dialectOfPath(path string) (dialect.Name, bool) {
	switch path {
	case dialect.OpenAIChat.LocalPath():
		return dialect.OpenAIChat, true
	case dialect.OpenAIResponses.LocalPath():
		return dialect.OpenAIResponses, true
	case dialect.ClaudeMessages.LocalPath():
		return dialect.ClaudeMessages, true
	}
	if strings.HasPrefix(path, geminiPrefix) {
		return dialect.GeminiGenerate, true
	}
	return "", false
}

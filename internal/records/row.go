package records

import (
	"strconv"
	"strings"
	"time"

	"github.com/tokenmp/core/dialect"
	"github.com/tokenmp/core/server"

	"github.com/tokenmp/cli/internal/metrics"
)

// 入口取值。web 的 request_records.entry 用这四个名字。
const (
	entryChat      = "chat"
	entryResponses = "responses"
	entryMessages  = "messages"
	entryGemini    = "gemini"
)

// Row 是 request_records 的一行，字段名与 web 的对接文档一致。
//
// 可空列用指针：nil 是不给，0 与空字符串是给了具体的值，两者在库里含义不同。
type Row struct {
	RequestID string `json:"request_id"`
	Status    string `json:"status"`
	Model     string `json:"model"`
	// ServedModel 与 Model 填同一个值：内核改写模型名之后参与匹配的是它，上游实际
	// 服务的也是它。
	ServedModel string `json:"served_model,omitempty"`
	Entry       string `json:"entry,omitempty"`
	Stream      bool   `json:"stream"`
	Channel     string `json:"channel,omitempty"`
	// ChannelType 是最后一次尝试用的方言，BaseURL 是最后一次尝试打到的上游。
	ChannelType string `json:"channel_type,omitempty"`
	BaseURL     string `json:"base_url,omitempty"`
	HTTPStatus  int    `json:"http_status"`
	// UpstreamCode 是最后一次尝试拿到的上游状态码，没拿到响应时留空。
	UpstreamCode string `json:"upstream_code,omitempty"`
	// ErrorCode 是最后一次尝试的失败原因（channel / convert / connect），没失败时留空。
	ErrorCode        string    `json:"error_code,omitempty"`
	Attempts         int       `json:"attempts"`
	Retried          bool      `json:"retried"`
	UserAgent        string    `json:"user_agent,omitempty"`
	LatencyMS        *int64    `json:"latency_ms,omitempty"`
	TTFBMS           *int64    `json:"ttfb_ms,omitempty"`
	TPS              *float64  `json:"tps,omitempty"`
	PromptTokens     *int      `json:"prompt_tokens,omitempty"`
	CompletionTokens *int      `json:"completion_tokens,omitempty"`
	CacheReadTokens  *int      `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens *int      `json:"cache_write_tokens,omitempty"`
	TotalTokens      *int      `json:"total_tokens,omitempty"`
	StartedAt        time.Time `json:"started_at"`
	FinishedAt       time.Time `json:"finished_at"`
	CreatedAt        time.Time `json:"created_at"`

	// UserID 与 APIKeyID 是 internal/clientauth 认出的调用方身份。核验没通过或校验
	// 端点不可用时按配置放行的请求不会走到这里：前者被中间件拦下、不写记录，后者
	// 认不出身份，两列为空。
	UserID   *int64 `json:"user_id,omitempty"`
	APIKeyID *int64 `json:"api_key_id,omitempty"`

	// ErrorMessage、UpstreamMessage、PanelMessage 恒为空：core 有意不把上游的错误
	// 文字带进公开面，原因文字只在它写出的 WARN 日志里。要落库得先让 core 把归一后
	// 的错误文字放进 Attempt。
	ErrorMessage    string `json:"error_message,omitempty"`
	UpstreamMessage string `json:"upstream_message,omitempty"`
	PanelMessage    string `json:"panel_message,omitempty"`
}

// FromOutcome 把 core 交出的原始量翻成一行。now 是回调触发的时刻，用来补时间戳：
// Outcome 不带时间，把它减去总时长得到开始时间。
func FromOutcome(outcome server.Outcome, now time.Time) Row {
	started := now.Add(-outcome.Duration)
	row := Row{
		RequestID:   outcome.RequestID,
		Status:      statusOf(outcome.Status),
		Model:       outcome.Model,
		ServedModel: outcome.Model,
		Entry:       entryOf(outcome.Path),
		Stream:      outcome.Streaming,
		Channel:     outcome.Channel,
		HTTPStatus:  outcome.Status,
		Attempts:    len(outcome.Attempts),
		Retried:     len(outcome.Attempts) > 1,
		UserAgent:   outcome.UserAgent,
		StartedAt:   started,
		FinishedAt:  now,
		CreatedAt:   started,
	}
	if last, ok := lastAttempt(outcome.Attempts); ok {
		row.ChannelType = string(last.Dialect)
		row.BaseURL = last.Upstream
		if last.Status > 0 {
			row.UpstreamCode = strconv.Itoa(last.Status)
		}
		if last.Failure != server.FailureNone {
			row.ErrorCode = string(last.Failure)
		}
	}

	latency := outcome.Duration.Milliseconds()
	row.LatencyMS = &latency
	// ttft 与 tps 的口径复用 internal/metrics，不在这里另算一遍：非流式请求的
	// FirstByte 约等于总时长，没有意义，metrics 已经把这种情况判掉。
	if ttft, ok := metrics.TTFT(outcome); ok {
		millis := ttft.Milliseconds()
		row.TTFBMS = &millis
	}
	if tps, ok := metrics.TPS(outcome); ok {
		row.TPS = &tps
	}
	if usage := outcome.Usage; usage != nil {
		prompt, completion := usage.PromptTokens, usage.CompletionTokens
		cacheRead, cacheWrite := usage.CachedTokens, usage.CacheWriteTokens
		total := usage.TotalTokens
		row.PromptTokens = &prompt
		row.CompletionTokens = &completion
		row.CacheReadTokens = &cacheRead
		row.CacheWriteTokens = &cacheWrite
		row.TotalTokens = &total
	}
	return row
}

// statusOf 把 HTTP 状态码折成 web 的 status 取值。写入方只在请求结束时调用，因此
// 不会出现 in_progress。
func statusOf(status int) string {
	if status < 400 {
		return "success"
	}
	return "error"
}

// entryOf 按请求路径判断入口。Gemini 的模型名与动作写在路径上，core 的 LocalPath
// 带 {model} 占位符，因此按前缀判断；其余三种路径固定，直接用 core 的说法比对。
func entryOf(path string) string {
	switch {
	case path == dialect.OpenAIChat.LocalPath():
		return entryChat
	case path == dialect.OpenAIResponses.LocalPath():
		return entryResponses
	case path == dialect.ClaudeMessages.LocalPath():
		return entryMessages
	case strings.HasPrefix(path, "/v1beta/models/"):
		return entryGemini
	default:
		return ""
	}
}

// lastAttempt 取最后一次尝试。没有尝试过时返回假值。
func lastAttempt(attempts []server.Attempt) (server.Attempt, bool) {
	if len(attempts) == 0 {
		return server.Attempt{}, false
	}
	return attempts[len(attempts)-1], true
}

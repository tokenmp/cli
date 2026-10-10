// Package metrics 把 core 交出的原始量整理成账目与指标行。
//
// core 只交原始量，不算指标：TTFT 从哪算、TPS 的分母取什么，口径随宿主变。这里定下
// 本工具的口径，见下面两个函数。
package metrics

import (
	"math"
	"time"

	"github.com/tokenmp/core/server"
)

// message 是账目与指标行的消息，也是收集端分流的依据。
const message = "用量统计"

// Message 返回这条日志的消息。
func Message() string { return message }

// Fields 返回账目与指标行的字段。没有可记的内容时返回 nil，调用方据此不写这一行。
func Fields(outcome server.Outcome) []any {
	fields := []any{
		"request_id", outcome.RequestID,
		"model", outcome.Model,
		"channel", outcome.Channel,
	}
	if outcome.Usage != nil {
		fields = append(fields,
			"prompt_tokens", outcome.Usage.PromptTokens,
			"completion_tokens", outcome.Usage.CompletionTokens,
			"total_tokens", outcome.Usage.TotalTokens)
		if outcome.Usage.CachedTokens > 0 {
			fields = append(fields, "cache_read_tokens", outcome.Usage.CachedTokens)
		}
		if rate, ok := CacheHitRate(*outcome.Usage); ok {
			fields = append(fields, "cache_hit_rate", rate)
		}
	}
	if ttft, ok := TTFT(outcome); ok {
		fields = append(fields, "ttft", ttft)
	}
	if tps, ok := TPS(outcome); ok {
		fields = append(fields, "tps", tps)
	}
	// 只有关联字段说明这次请求既没有用量也没有流式指标，写出来只是噪声。
	if len(fields) == 6 {
		return nil
	}
	return fields
}

// CacheHitRate 返回缓存命中率，取值在 0 到 1 之间，保留三位小数。
//
// 分母是提示词总数，它含缓存读与缓存写：上游的原始计数里三者是分开的，归一之后
// 提示词总数才是完整的那份。分子只算缓存读，缓存写是新写入的部分，不算命中。
//
// 只在缓存读大于零时给值。上游不报缓存用量时这个字段也是零，与「报了但没命中」
// 分不开，宁可不写，也不写一个可能读错意思的 0。
func CacheHitRate(usage server.Usage) (float64, bool) {
	if usage.CachedTokens <= 0 || usage.PromptTokens <= 0 {
		return 0, false
	}
	rate := float64(usage.CachedTokens) / float64(usage.PromptTokens)
	return math.Round(rate*1000) / 1000, true
}

// TTFT 返回首字节延迟，只对流式请求给值。
//
// 非流式响应是一次写完的，core 记下的首字节时间约等于总时长，看不出首字节延迟，
// 这个数没有意义。core 也不为这类请求填它（整个请求没写出过响应体时为 0）。
func TTFT(outcome server.Outcome) (time.Duration, bool) {
	if !outcome.Streaming || outcome.FirstByte <= 0 {
		return 0, false
	}
	return outcome.FirstByte, true
}

// TPS 用生成时长算生成速率，保留一位小数。
//
// 分母取生成时长（总时长减首字节延迟），不取总时长：预填充与排队都不该算进生成速率，
// 长提示词上按总时长算会明显偏低。非流式请求里预填充与生成分不开，因此不给这个数。
// 拿不到用量、用量里没有生成 token、或生成时长为零时也不给。
func TPS(outcome server.Outcome) (float64, bool) {
	if outcome.Usage == nil || outcome.Usage.CompletionTokens <= 0 {
		return 0, false
	}
	ttft, ok := TTFT(outcome)
	if !ok {
		return 0, false
	}
	generating := outcome.Duration - ttft
	if generating <= 0 {
		return 0, false
	}
	rate := float64(outcome.Usage.CompletionTokens) / generating.Seconds()
	return math.Round(rate*10) / 10, true
}

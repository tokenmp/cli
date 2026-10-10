package metrics

import (
	"testing"
	"time"

	"github.com/tokenmp/core/server"
)

// fieldValue 从字段表里取一个值，取不到时返回 nil。
func fieldValue(fields []any, key string) any {
	for index := 0; index+1 < len(fields); index += 2 {
		if fields[index] == key {
			return fields[index+1]
		}
	}
	return nil
}

func TestFieldsStreamingWithUsage(t *testing.T) {
	outcome := server.Outcome{
		RequestID: "7",
		Model:     "deepseek-chat",
		Channel:   "commandcode",
		Streaming: true,
		Duration:  10 * time.Second,
		FirstByte: 2 * time.Second,
		Usage:     &server.Usage{PromptTokens: 20696, CompletionTokens: 74, TotalTokens: 20770, CachedTokens: 20000},
	}

	fields := Fields(outcome)
	if fields == nil {
		t.Fatal("应当有可记的内容")
	}

	// 生成时长是 8 秒，74 个 token 因此是每秒 9.25 个，保留一位小数。
	if got := fieldValue(fields, "ttft"); got != 2*time.Second {
		t.Errorf("ttft 是 %v，期望 2s", got)
	}
	if got := fieldValue(fields, "tps"); got != 9.3 {
		t.Errorf("tps 是 %v，期望 9.3", got)
	}
	for key, want := range map[string]any{
		"request_id": "7", "model": "deepseek-chat", "channel": "commandcode",
		"prompt_tokens": 20696, "completion_tokens": 74, "total_tokens": 20770,
		"cache_read_tokens": 20000, "cache_hit_rate": 0.966,
	} {
		if got := fieldValue(fields, key); got != want {
			t.Errorf("%s 是 %v，期望 %v", key, got, want)
		}
	}
}

func TestFieldsNonStreamingHasNoRate(t *testing.T) {
	// 非流式响应一次写完，首字节时间约等于总时长，TTFT 与 TPS 都不给。
	outcome := server.Outcome{
		RequestID: "8",
		Duration:  2 * time.Second,
		FirstByte: 2 * time.Second,
		Usage:     &server.Usage{CompletionTokens: 100, TotalTokens: 100},
	}

	fields := Fields(outcome)
	if fields == nil {
		t.Fatal("有用量就该记")
	}
	if got := fieldValue(fields, "ttft"); got != nil {
		t.Errorf("非流式请求不该有 ttft: %v", got)
	}
	if got := fieldValue(fields, "tps"); got != nil {
		t.Errorf("非流式请求不该有 tps: %v", got)
	}
	if got := fieldValue(fields, "completion_tokens"); got != 100 {
		t.Errorf("用量仍要记: %v", got)
	}
}

func TestFieldsStreamingWithoutUsage(t *testing.T) {
	// 流式请求有首字节时间但没有用量：只记 TTFT。
	outcome := server.Outcome{
		RequestID: "9",
		Streaming: true,
		Duration:  5 * time.Second,
		FirstByte: 800 * time.Millisecond,
	}

	fields := Fields(outcome)
	if got := fieldValue(fields, "ttft"); got != 800*time.Millisecond {
		t.Errorf("ttft 是 %v，期望 800ms", got)
	}
	if got := fieldValue(fields, "tps"); got != nil {
		t.Errorf("没有用量不该有 tps: %v", got)
	}
	if got := fieldValue(fields, "total_tokens"); got != nil {
		t.Errorf("没有用量不该有 token 字段: %v", got)
	}
}

func TestFieldsNothingToRecord(t *testing.T) {
	// 同方言透传：没有用量也没有流式指标，写出来只是噪声。
	outcome := server.Outcome{RequestID: "10", Model: "gpt-4o", Channel: "a", Duration: time.Second}
	if fields := Fields(outcome); fields != nil {
		t.Errorf("没有可记的内容时应当返回 nil: %v", fields)
	}
}

func TestFieldsZeroGenerationWindow(t *testing.T) {
	// 首字节与结束同时发生，生成时长为零，不给速率。
	outcome := server.Outcome{
		RequestID: "11",
		Streaming: true,
		Duration:  time.Second,
		FirstByte: time.Second,
		Usage:     &server.Usage{CompletionTokens: 5},
	}

	fields := Fields(outcome)
	if got := fieldValue(fields, "tps"); got != nil {
		t.Errorf("生成时长为零时不该有 tps: %v", got)
	}
	if got := fieldValue(fields, "ttft"); got != time.Second {
		t.Errorf("ttft 仍应记下: %v", got)
	}
}

func TestFieldsCache(t *testing.T) {
	// 提示词总数 1000，其中 640 命中缓存，命中率 0.64。
	outcome := server.Outcome{
		RequestID: "12",
		Usage:     &server.Usage{PromptTokens: 1000, TotalTokens: 1010, CachedTokens: 640},
	}
	fields := Fields(outcome)
	if got := fieldValue(fields, "cache_read_tokens"); got != 640 {
		t.Errorf("cache_read_tokens 是 %v，期望 640", got)
	}
	if got := fieldValue(fields, "cache_hit_rate"); got != 0.64 {
		t.Errorf("cache_hit_rate 是 %v，期望 0.64", got)
	}

	// 上游不报缓存用量时这两个字段都不写：分不出「没命中」与「不报」。
	outcome.Usage.CachedTokens = 0
	fields = Fields(outcome)
	if got := fieldValue(fields, "cache_read_tokens"); got != nil {
		t.Errorf("没有缓存读时不该有 cache_read_tokens: %v", got)
	}
	if got := fieldValue(fields, "cache_hit_rate"); got != nil {
		t.Errorf("没有缓存读时不该有 cache_hit_rate: %v", got)
	}

	// 命中率保留三位小数。
	outcome.Usage = &server.Usage{PromptTokens: 3, TotalTokens: 3, CachedTokens: 1}
	if got := fieldValue(Fields(outcome), "cache_hit_rate"); got != 0.333 {
		t.Errorf("cache_hit_rate 是 %v，期望 0.333", got)
	}
}

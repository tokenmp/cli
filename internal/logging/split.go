package logging

import (
	"context"
	"log/slog"
	"strings"
)

// usageMessage 是账目行的消息，也是这条流水线的分流依据。
const usageMessage = "用量统计"

// usageSuffix 用来认出用量字段。core 把用量折在完成行里（prompt_tokens、
// completion_tokens、total_tokens、cached_tokens），以后加 reasoning_tokens
// 这类字段也能自动跟上，不必在这里维护名单。
const usageSuffix = "_tokens"

// joinKeys 是账目行要一并带上的字段：靠 request_id 与完成行对上，靠 model 与
// channel 按模型、渠道汇总。
var joinKeys = map[string]bool{"request_id": true, "model": true, "channel": true}

// usageSplitter 把完成行里的用量字段拆成单独一行。
//
// core 的设计是把用量交给访问日志那一行（见 core 的 server/access.go），因此
// 用量默认出现在完成行里。宿主想单独收集账目，就在写出之前把这条记录拆成两条：
// 一条不含用量字段的完成行，一条只带账目的用量行，两条共用同一个时间戳与
// request_id，日志里仍然对得上。
//
// 只拆「带用量字段」的记录，其余原样透传。消息已经是账目行的记录也不再拆，
// 这样 core 以后自己加账目行时，这里不会把它拆成一条空行加一条新行。
type usageSplitter struct {
	inner slog.Handler
}

// splitUsage 包住真正的 handler。
func splitUsage(inner slog.Handler) slog.Handler {
	return usageSplitter{inner: inner}
}

func (h usageSplitter) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h usageSplitter) WithAttrs(attrs []slog.Attr) slog.Handler {
	return usageSplitter{inner: h.inner.WithAttrs(attrs)}
}

func (h usageSplitter) WithGroup(name string) slog.Handler {
	return usageSplitter{inner: h.inner.WithGroup(name)}
}

func (h usageSplitter) Handle(ctx context.Context, record slog.Record) error {
	// 账目行记 INFO：它是账，不是故障；请求失败时状态码那一行已经记成 WARN。
	// 级别过滤仍由内层 handler 决定，log.level 调到 warn 时这行会被丢掉。
	access := slog.NewRecord(record.Time, record.Level, record.Message, record.PC)
	usage := slog.NewRecord(record.Time, slog.LevelInfo, usageMessage, record.PC)

	hasUsage := false
	record.Attrs(func(attr slog.Attr) bool {
		switch {
		case strings.HasSuffix(attr.Key, usageSuffix):
			hasUsage = true
			usage.AddAttrs(attr)
		default:
			access.AddAttrs(attr)
			if joinKeys[attr.Key] {
				usage.AddAttrs(attr)
			}
		}
		return true
	})

	if err := h.inner.Handle(ctx, access); err != nil {
		return err
	}
	if !hasUsage || record.Message == usageMessage {
		return nil
	}
	return h.inner.Handle(ctx, usage)
}

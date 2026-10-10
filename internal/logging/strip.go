package logging

import (
	"context"
	"log/slog"
	"strings"
)

// usageSuffix 用来认出用量字段。core 把用量折在完成行里（prompt_tokens、
// completion_tokens、total_tokens、cached_tokens），以后加 reasoning_tokens
// 这类字段也能自动跟上，不必在这里维护名单。
const usageSuffix = "_tokens"

// usageStripper 把完成行里的用量字段去掉。
//
// core 的设计是把用量交给访问日志那一行（见 core 的 server/access.go），而账目由
// 宿主从 OnRequestDone 里单独写一行：那边能同时拿到首字节时间这类只有 core 知道的
// 原始量。两处都写就成了两份账，因此在写出之前把完成行里的 token 字段摘掉。
//
// 只动带用量字段的记录，其余原样透传。
type usageStripper struct {
	inner slog.Handler
}

// stripUsage 包住真正的 handler。
func stripUsage(inner slog.Handler) slog.Handler {
	return usageStripper{inner: inner}
}

func (h usageStripper) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h usageStripper) WithAttrs(attrs []slog.Attr) slog.Handler {
	return usageStripper{inner: h.inner.WithAttrs(attrs)}
}

func (h usageStripper) WithGroup(name string) slog.Handler {
	return usageStripper{inner: h.inner.WithGroup(name)}
}

func (h usageStripper) Handle(ctx context.Context, record slog.Record) error {
	if !hasUsage(record) {
		return h.inner.Handle(ctx, record)
	}

	kept := slog.NewRecord(record.Time, record.Level, record.Message, record.PC)
	record.Attrs(func(attr slog.Attr) bool {
		if !strings.HasSuffix(attr.Key, usageSuffix) {
			kept.AddAttrs(attr)
		}
		return true
	})
	return h.inner.Handle(ctx, kept)
}

// hasUsage 报告记录里有没有用量字段。
func hasUsage(record slog.Record) bool {
	found := false
	record.Attrs(func(attr slog.Attr) bool {
		if strings.HasSuffix(attr.Key, usageSuffix) {
			found = true
			return false
		}
		return true
	})
	return found
}

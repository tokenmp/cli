// Package logging 按配置构造服务日志。
//
// 服务日志与命令输出走两条路：命令输出给人看，带配色；服务日志是 slog 的结构化
// 输出，写标准错误，方便被收集。格式与级别都来自配置里的 log 段。
package logging

import (
	"io"
	"log/slog"

	"github.com/tokenmp/core/config"
)

// Loggers 是给两边的日志入口。
//
// 服务日志由 core 写，账目与指标由本工具从 OnRequestDone 里写。两者写同一个输出，
// 但 core 的完成行里折着用量字段，而账目已经单独成行，因此只给 core 的那个入口包
// 一层摘除。见 strip.go。
type Loggers struct {
	// Core 交给 server.FromConfig，它的完成行里不带 token 字段。
	Core *slog.Logger
	// Own 用来写账目与指标行，不经过摘除。
	Own *slog.Logger
}

// New 按配置里的格式与级别构造日志。写到哪个输出由调用方决定。
//
// format 与 level 的取值在 config.Validate 里已经卡过，这里只做映射，遇到意外值
// 退回到 text 与 info，不额外报错。
func New(log config.Log, w io.Writer) Loggers {
	return NewWithCoreLevel(log, w, level(log.Level))
}

// NewWithCoreLevel 与 New 相同，但把 core 的日志单独设到 coreLevel。
//
// test 命令默认用它把 core 压到 warn，只留错误；--verbose 时改回 New，按配置里的
// 级别放开。账目与指标入口 Own 始终按配置里的级别。
func NewWithCoreLevel(log config.Log, w io.Writer, coreLevel slog.Level) Loggers {
	return Loggers{
		Core: slog.New(stripUsage(handler(log.Format, w, &slog.HandlerOptions{Level: coreLevel}))),
		Own:  slog.New(handler(log.Format, w, &slog.HandlerOptions{Level: level(log.Level)})),
	}
}

// handler 按格式造一个 slog 处理器。format 不认识时用 text。
func handler(format string, w io.Writer, options *slog.HandlerOptions) slog.Handler {
	if format == "json" {
		return slog.NewJSONHandler(w, options)
	}
	return slog.NewTextHandler(w, options)
}

// level 把配置里的级别名映射成 slog 的级别。
func level(name string) slog.Level {
	switch name {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

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
	options := &slog.HandlerOptions{Level: level(log.Level)}
	var handler slog.Handler
	if log.Format == "json" {
		handler = slog.NewJSONHandler(w, options)
	} else {
		handler = slog.NewTextHandler(w, options)
	}
	return Loggers{
		Core: slog.New(stripUsage(handler)),
		Own:  slog.New(handler),
	}
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

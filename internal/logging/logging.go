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

// New 按配置里的格式与级别构造日志。写到哪个输出由调用方决定。
//
// format 与 level 的取值在 config.Validate 里已经卡过，这里只做映射，遇到意外值
// 退回到 text 与 info，不额外报错。
func New(log config.Log, w io.Writer) *slog.Logger {
	options := &slog.HandlerOptions{Level: level(log.Level)}
	var handler slog.Handler
	if log.Format == "json" {
		handler = slog.NewJSONHandler(w, options)
	} else {
		handler = slog.NewTextHandler(w, options)
	}
	return slog.New(handler)
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

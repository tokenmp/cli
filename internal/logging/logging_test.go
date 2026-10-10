package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/tokenmp/core/config"
)

func TestNewTextFormatFiltersByLevel(t *testing.T) {
	var buf bytes.Buffer
	logger := New(config.Log{Format: "text", Level: "info"}, &buf)

	logger.Debug("调试信息不该出现")
	logger.Info("开始监听", "address", "127.0.0.1:8787")

	out := buf.String()
	if strings.Contains(out, "调试信息不该出现") {
		t.Errorf("info 级别下 debug 日志不该输出:\n%s", out)
	}
	if !strings.Contains(out, "开始监听") || !strings.Contains(out, "address=127.0.0.1:8787") {
		t.Errorf("text 格式的日志形状不对:\n%s", out)
	}
}

func TestNewJSONFormat(t *testing.T) {
	var buf bytes.Buffer
	logger := New(config.Log{Format: "json", Level: "warn"}, &buf)

	logger.Info("不该出现")
	logger.Warn("凭据缺失", "channel", "example")

	var record map[string]any
	if err := json.Unmarshal(buf.Bytes(), &record); err != nil {
		t.Fatalf("json 格式的日志解析失败: %v\n%s", err, buf.String())
	}
	if record["level"] != "WARN" || record["msg"] != "凭据缺失" || record["channel"] != "example" {
		t.Errorf("json 格式的日志字段不对: %v", record)
	}
}

func TestLevelMapping(t *testing.T) {
	cases := map[string]slog.Level{
		"debug": slog.LevelDebug,
		"info":  slog.LevelInfo,
		"warn":  slog.LevelWarn,
		"error": slog.LevelError,
		// 校验已经卡过取值，这里只验证意外值退回到 info。
		"":        slog.LevelInfo,
		"verbose": slog.LevelInfo,
	}
	for name, want := range cases {
		if got := level(name); got != want {
			t.Errorf("level(%q) = %v，期望 %v", name, got, want)
		}
	}
}

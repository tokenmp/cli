package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

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

func TestSplitUsage(t *testing.T) {
	var buf bytes.Buffer
	logger := New(config.Log{Format: "text", Level: "info"}, &buf)

	logger.Info("请求完成", "request_id", "1", "method", "POST", "path", "/v1/messages",
		"status", 200, "duration", 11*time.Second, "model", "deepseek-v4.1-flash",
		"channel", "commandcode", "attempts", 1, "prompt_tokens", 20696,
		"completion_tokens", 74, "total_tokens", 20770)

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("应当拆成两条，实际 %d 条:\n%s", len(lines), buf.String())
	}

	// 完成行不再带用量字段，其余字段照旧。
	for _, gone := range []string{"prompt_tokens", "completion_tokens", "total_tokens"} {
		if strings.Contains(lines[0], gone) {
			t.Errorf("完成行里不该有 %q:\n%s", gone, lines[0])
		}
	}
	for _, want := range []string{"msg=请求完成", "request_id=1", "status=200", "channel=commandcode"} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("完成行里没有 %q:\n%s", want, lines[0])
		}
	}

	// 账目行只带关联字段与用量，并带上同一个 request_id。
	for _, want := range []string{"msg=用量统计", "request_id=1", "model=deepseek-v4.1-flash",
		"channel=commandcode", "prompt_tokens=20696", "completion_tokens=74", "total_tokens=20770"} {
		if !strings.Contains(lines[1], want) {
			t.Errorf("账目行里没有 %q:\n%s", want, lines[1])
		}
	}
	for _, gone := range []string{"method", "path", "duration", "user_agent"} {
		if strings.Contains(lines[1], gone) {
			t.Errorf("账目行里不该有 %q:\n%s", gone, lines[1])
		}
	}
}

func TestSplitUsageLeavesOtherRecords(t *testing.T) {
	var buf bytes.Buffer
	logger := New(config.Log{Format: "text", Level: "info"}, &buf)

	// 没有用量的完成行（同方言透传、/healthz）只有一条。
	logger.Info("请求完成", "request_id", "2", "path", "/healthz", "status", 200)
	// 账目行本身不再被拆。
	logger.Info("用量统计", "request_id", "3", "prompt_tokens", 5, "total_tokens", 5)

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("应当只有两条，实际 %d 条:\n%s", len(lines), buf.String())
	}
	if !strings.Contains(lines[0], "msg=请求完成") || !strings.Contains(lines[1], "msg=用量统计") {
		t.Errorf("两条的顺序或内容不对:\n%s", buf.String())
	}
}

func TestSplitUsageJSON(t *testing.T) {
	var buf bytes.Buffer
	logger := New(config.Log{Format: "json", Level: "info"}, &buf)

	logger.Info("请求完成", "request_id", "1", "status", 200, "model", "m", "prompt_tokens", 7)

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("应当拆成两条 JSON，实际 %d 条:\n%s", len(lines), buf.String())
	}

	var access, usage map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &access); err != nil {
		t.Fatalf("第一条不是合法 JSON: %v\n%s", err, lines[0])
	}
	if err := json.Unmarshal([]byte(lines[1]), &usage); err != nil {
		t.Fatalf("第二条不是合法 JSON: %v\n%s", err, lines[1])
	}
	if _, ok := access["prompt_tokens"]; ok {
		t.Errorf("完成行里不该有 prompt_tokens: %v", access)
	}
	if usage["msg"] != "用量统计" || usage["prompt_tokens"] != float64(7) || usage["request_id"] != "1" {
		t.Errorf("账目行的字段不对: %v", usage)
	}
}

func TestSplitUsageRespectsLevel(t *testing.T) {
	var buf bytes.Buffer
	logger := New(config.Log{Format: "text", Level: "warn"}, &buf)

	logger.Info("请求完成", "request_id", "1", "prompt_tokens", 7)

	if buf.Len() != 0 {
		t.Errorf("warn 级别下不该写出 info 记录:\n%s", buf.String())
	}
}

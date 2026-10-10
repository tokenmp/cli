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
	logger := New(config.Log{Format: "text", Level: "info"}, &buf).Core

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
	logger := New(config.Log{Format: "json", Level: "warn"}, &buf).Core

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

func TestStripUsage(t *testing.T) {
	var buf bytes.Buffer
	logger := New(config.Log{Format: "text", Level: "info"}, &buf).Core

	logger.Info("请求完成", "request_id", "1", "method", "POST", "path", "/v1/messages",
		"status", 200, "duration", 11*time.Second, "model", "deepseek-v4.1-flash",
		"channel", "commandcode", "attempts", 1, "prompt_tokens", 20696,
		"completion_tokens", 74, "total_tokens", 20770)

	out := buf.String()
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 1 {
		t.Fatalf("完成行应当只有一条，实际 %d 条:\n%s", len(lines), out)
	}
	for _, gone := range []string{"prompt_tokens", "completion_tokens", "total_tokens"} {
		if strings.Contains(out, gone) {
			t.Errorf("完成行里不该有 %q:\n%s", gone, out)
		}
	}
	// 其余字段照旧，账目行由 run 从 OnRequestDone 写，不在这里。
	for _, want := range []string{"msg=请求完成", "request_id=1", "status=200",
		"model=deepseek-v4.1-flash", "channel=commandcode"} {
		if !strings.Contains(out, want) {
			t.Errorf("完成行里没有 %q:\n%s", want, out)
		}
	}
}

func TestStripUsageLeavesOtherRecords(t *testing.T) {
	var buf bytes.Buffer
	logger := New(config.Log{Format: "text", Level: "info"}, &buf).Core

	// 没有用量的记录（同方言透传、/healthz、失败行）原样写出。
	logger.Info("请求完成", "request_id", "2", "path", "/healthz", "status", 200)
	logger.Warn("上游请求失败", "request_id", "3", "error", "连接超时")

	out := buf.String()
	if !strings.Contains(out, "msg=请求完成") || !strings.Contains(out, "msg=上游请求失败") {
		t.Errorf("两条记录都该原样写出:\n%s", out)
	}
	if strings.Count(out, "\n") != 2 {
		t.Errorf("应当只有两条:\n%s", out)
	}
}

func TestStripUsageJSON(t *testing.T) {
	var buf bytes.Buffer
	logger := New(config.Log{Format: "json", Level: "info"}, &buf).Core

	logger.Info("请求完成", "request_id", "1", "status", 200, "model", "m",
		"prompt_tokens", 7, "total_tokens", 7)

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("应当只有一条 JSON，实际 %d 条:\n%s", len(lines), buf.String())
	}

	var record map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &record); err != nil {
		t.Fatalf("不是合法 JSON: %v\n%s", err, lines[0])
	}
	for _, gone := range []string{"prompt_tokens", "total_tokens"} {
		if _, ok := record[gone]; ok {
			t.Errorf("JSON 里不该有 %q: %v", gone, record)
		}
	}
	if record["msg"] != "请求完成" || record["status"] != float64(200) || record["model"] != "m" {
		t.Errorf("其余字段不对: %v", record)
	}
}

func TestStripUsageRespectsLevel(t *testing.T) {
	var buf bytes.Buffer
	logger := New(config.Log{Format: "text", Level: "warn"}, &buf).Core

	logger.Info("请求完成", "request_id", "1", "prompt_tokens", 7)

	if buf.Len() != 0 {
		t.Errorf("warn 级别下不该写出 info 记录:\n%s", buf.String())
	}
}

func TestOwnLoggerKeepsUsage(t *testing.T) {
	var buf bytes.Buffer
	logs := New(config.Log{Format: "text", Level: "info"}, &buf)

	// 账目行由本工具自己写，不经过摘除：这条回归测试对应一次真实故障，
	// 当时两边的日志共用一个入口，账目行里的 token 字段被自己摘掉了。
	logs.Own.Info("用量统计", "request_id", "1", "model", "m", "prompt_tokens", 20696, "total_tokens", 20770)

	out := buf.String()
	for _, want := range []string{"prompt_tokens=20696", "total_tokens=20770", "model=m"} {
		if !strings.Contains(out, want) {
			t.Errorf("自己写的账目行不该被摘除，缺 %q:\n%s", want, out)
		}
	}

	// core 的那条仍然摘掉。
	buf.Reset()
	logs.Core.Info("请求完成", "request_id", "1", "status", 200, "prompt_tokens", 20696)
	if strings.Contains(buf.String(), "prompt_tokens") {
		t.Errorf("core 的完成行仍应摘除 token 字段:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "status=200") {
		t.Errorf("其余字段应当保留:\n%s", buf.String())
	}
}

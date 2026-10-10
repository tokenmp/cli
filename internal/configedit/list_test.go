package configedit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testChannel 是测试用的条目形状，字段顺序与真实配置一致。
type testChannel struct {
	Name    string `yaml:"name"`
	Dialect string `yaml:"dialect"`
	BaseURL string `yaml:"base_url,omitempty"`
}

// testPlugin 用来验证嵌套路径，字段顺序与 core 的插件条目一致。
type testPlugin struct {
	Key     string `yaml:"key"`
	Enabled bool   `yaml:"enabled"`
}

// sandbox 写一份配置并返回路径。
func sandbox(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("写测试配置失败: %v", err)
	}
	return path
}

// readBack 读回文件内容。
func readBack(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读回配置失败: %v", err)
	}
	return string(data)
}

func TestAppendMappingKeepsCommentsAndIndent(t *testing.T) {
	const source = `# 服务配置
listen: 127.0.0.1:9999

channels:
  # 已有的渠道
  - name: example
    dialect: openai
    base_url: https://api.example.com/v1
`
	path := sandbox(t, source)
	err := AppendMapping(path, []string{"channels"}, testChannel{
		Name:    "extra",
		Dialect: "claude",
		BaseURL: "https://api.anthropic.com",
	}, nil)
	if err != nil {
		t.Fatalf("AppendMapping: %v", err)
	}

	// 空行会被编码规范化，注释与两格缩进保留，见包文档。
	const want = `# 服务配置
listen: 127.0.0.1:9999
channels:
  # 已有的渠道
  - name: example
    dialect: openai
    base_url: https://api.example.com/v1
  - name: extra
    dialect: claude
    base_url: https://api.anthropic.com
`
	if got := readBack(t, path); got != want {
		t.Errorf("追加结果不对\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestAppendMappingToEmptySequence(t *testing.T) {
	path := sandbox(t, "listen: 127.0.0.1:9999\nchannels: []\n")
	err := AppendMapping(path, []string{"channels"}, testChannel{
		Name:    "extra",
		Dialect: "claude",
	}, nil)
	if err != nil {
		t.Fatalf("AppendMapping: %v", err)
	}

	const want = `listen: 127.0.0.1:9999
channels:
  - name: extra
    dialect: claude
`
	if got := readBack(t, path); got != want {
		t.Errorf("空序列追加结果不对\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestAppendMappingNestedPath(t *testing.T) {
	path := sandbox(t, "plugins:\n  dir: plugins\n  list: []\n")
	err := AppendMapping(path, []string{"plugins", "list"}, testPlugin{
		Key:     "logger",
		Enabled: true,
	}, nil)
	if err != nil {
		t.Fatalf("AppendMapping: %v", err)
	}
	got := readBack(t, path)
	for _, want := range []string{"plugins:", "list:", "- key: logger", "enabled: true", "dir: plugins"} {
		if !strings.Contains(got, want) {
			t.Errorf("结果里没有 %q:\n%s", want, got)
		}
	}
}

func TestRemoveIndex(t *testing.T) {
	const source = `channels:
  # 第一条
  - name: first
    dialect: openai
  - name: second
    dialect: claude
`
	path := sandbox(t, source)
	if err := RemoveIndex(path, []string{"channels"}, 0, nil); err != nil {
		t.Fatalf("RemoveIndex: %v", err)
	}

	const want = `channels:
  - name: second
    dialect: claude
`
	if got := readBack(t, path); got != want {
		t.Errorf("删除结果不对\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestRemoveIndexLastLeavesEmptySequence(t *testing.T) {
	path := sandbox(t, "channels:\n  - name: only\n    dialect: openai\n")
	if err := RemoveIndex(path, []string{"channels"}, 0, nil); err != nil {
		t.Fatalf("RemoveIndex: %v", err)
	}

	const want = "channels: []\n"
	if got := readBack(t, path); got != want {
		t.Errorf("删除唯一一条后应当是空序列\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestRemoveIndexOutOfRange(t *testing.T) {
	path := sandbox(t, "channels:\n  - name: only\n    dialect: openai\n")
	err := RemoveIndex(path, []string{"channels"}, 5, nil)
	if err == nil {
		t.Fatal("越界应当报错")
	}
	for _, want := range []string{"路径 channels", "下标 5", "越界"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("错误信息 %q 里没有 %q", err.Error(), want)
		}
	}

	// 负数同样按越界处理。
	if err := RemoveIndex(path, []string{"channels"}, -1, nil); err == nil {
		t.Fatal("负数下标应当报错")
	}
}

func TestRemoveIndexCheckFailureRestores(t *testing.T) {
	const source = "channels:\n  - name: only\n    dialect: openai\n"
	path := sandbox(t, source)

	want := os.ErrInvalid
	err := RemoveIndex(path, []string{"channels"}, 0, func() error { return want })
	if err == nil {
		t.Fatal("校验失败应当报错")
	}
	if !strings.Contains(err.Error(), "已还原") {
		t.Errorf("错误信息应当说明已还原: %v", err)
	}
	if got := readBack(t, path); got != source {
		t.Errorf("校验失败后文件没有还原\n--- got ---\n%s\n--- want ---\n%s", got, source)
	}
}

func TestAppendMappingCheckFailureRestores(t *testing.T) {
	const source = "# 顶部注释\nchannels: []\n"
	path := sandbox(t, source)

	err := AppendMapping(path, []string{"channels"}, testChannel{Name: "extra"}, func() error {
		return os.ErrInvalid
	})
	if err == nil {
		t.Fatal("校验失败应当报错")
	}
	if !strings.Contains(err.Error(), "已还原") {
		t.Errorf("错误信息应当说明已还原: %v", err)
	}
	if got := readBack(t, path); got != source {
		t.Errorf("校验失败后文件没有还原\n--- got ---\n%s\n--- want ---\n%s", got, source)
	}
}

func TestListPathMissing(t *testing.T) {
	path := sandbox(t, "listen: 127.0.0.1:9999\n")

	// 最后一段不存在时补出来：channels create 要能在没有 channels 段的配置上直接追加。
	err := AppendMapping(path, []string{"channels"}, testChannel{Name: "extra", Dialect: "claude"}, nil)
	if err != nil {
		t.Fatalf("最后一段不存在时应当补出来: %v", err)
	}
	const want = "listen: 127.0.0.1:9999\nchannels:\n  - name: extra\n    dialect: claude\n"
	if got := readBack(t, path); got != want {
		t.Errorf("补出的序列形状不对\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}

	// 中间段不存在仍然报错，不猜结构。
	err = RemoveIndex(path, []string{"plugins", "list"}, 0, nil)
	if err == nil || !strings.Contains(err.Error(), "路径 plugins 不存在") {
		t.Errorf("中间段不存在应当报错并点出路径: %v", err)
	}
	err = AppendMapping(path, []string{"plugins", "list"}, testPlugin{Key: "echo"}, nil)
	if err == nil || !strings.Contains(err.Error(), "路径 plugins 不存在") {
		t.Errorf("追加时中间段不存在也应当报错: %v", err)
	}
}

func TestAppendMappingCreatesMissingSequenceInExistingMapping(t *testing.T) {
	// plugins 段在、list 不在时补出 list。
	const source = "listen: 127.0.0.1:9999\n\nplugins:\n  dir: plugins\n"
	path := sandbox(t, source)

	if err := AppendMapping(path, []string{"plugins", "list"}, testPlugin{Key: "echo", Enabled: true}, nil); err != nil {
		t.Fatalf("AppendMapping: %v", err)
	}
	// 空行会被编码规范化，注释与两格缩进保留，见包文档。
	const want = "listen: 127.0.0.1:9999\nplugins:\n  dir: plugins\n  list:\n    - key: echo\n      enabled: true\n"
	if got := readBack(t, path); got != want {
		t.Errorf("嵌套补齐的形状不对\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestAppendMappingKeepsExistingEntries(t *testing.T) {
	// 已经有 channels 段时仍然追加到末尾，不重建序列。
	const source = "channels:\n  # 已有的一条\n  - name: example\n    dialect: openai\n"
	path := sandbox(t, source)

	if err := AppendMapping(path, []string{"channels"}, testChannel{Name: "extra", Dialect: "claude"}, nil); err != nil {
		t.Fatalf("AppendMapping: %v", err)
	}
	const want = "channels:\n  # 已有的一条\n  - name: example\n    dialect: openai\n  - name: extra\n    dialect: claude\n"
	if got := readBack(t, path); got != want {
		t.Errorf("追加结果不对\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestListPathNotSequence(t *testing.T) {
	path := sandbox(t, "listen: 127.0.0.1:9999\n")

	err := AppendMapping(path, []string{"listen"}, testChannel{Name: "extra"}, nil)
	if err == nil || !strings.Contains(err.Error(), "路径 listen 不是序列") {
		t.Errorf("标量路径应当报错: %v", err)
	}

	err = RemoveIndex(path, []string{"listen"}, 0, nil)
	if err == nil || !strings.Contains(err.Error(), "路径 listen 不是序列") {
		t.Errorf("标量路径应当报错: %v", err)
	}
}

func TestListEmptyPath(t *testing.T) {
	path := sandbox(t, "channels: []\n")

	if err := AppendMapping(path, nil, testChannel{Name: "extra"}, nil); err == nil {
		t.Error("空路径应当报错")
	}
	if err := RemoveIndex(path, nil, 0, nil); err == nil {
		t.Error("空路径应当报错")
	}
}

func TestAppendMappingRejectsNonMapping(t *testing.T) {
	path := sandbox(t, "channels: []\n")

	err := AppendMapping(path, []string{"channels"}, "not-a-mapping", nil)
	if err == nil || !strings.Contains(err.Error(), "路径 channels 的条目不是映射") {
		t.Errorf("非映射条目应当报错: %v", err)
	}
}

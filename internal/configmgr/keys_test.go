package configmgr

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/tokenmp/core/config"
)

// TestSettablePathsAreKnownKeys 断言可写白名单是 core 键表路径集合的子集。
//
// 键表里没有「是否可 set」这一列：那是 config set 的策略，不是配置本身的事实，因此
// 白名单由 CLI 自己维护。这条盯着两边不漂：core 改了字段名或删了字段，白名单里留下
// 的旧路径就成了永远改不动的死项。
func TestSettablePathsAreKnownKeys(t *testing.T) {
	known := map[string]bool{}
	for _, key := range config.Keys() {
		known[key.Path] = true
	}
	for _, path := range SettablePaths {
		if !known[path] {
			t.Errorf("白名单里的 %s 不在 core 的键表里", path)
		}
	}
}

// TestSettablePathsAreTopLevelScalars 白名单里只能是顶层标量：带下标的路径改不了，
// 写回只走映射（见 Set）。
func TestSettablePathsAreTopLevelScalars(t *testing.T) {
	scalars := map[string]bool{}
	for _, path := range ScalarPaths() {
		scalars[path] = true
	}
	for _, path := range SettablePaths {
		if !scalars[path] {
			t.Errorf("白名单里的 %s 不是顶层标量", path)
		}
	}
}

// TestScalarPathsFollowKeys 顶层标量取自键表：路径里不含 []，且键表里的顶层标量一个不落。
func TestScalarPathsFollowKeys(t *testing.T) {
	want := map[string]bool{}
	for _, key := range config.Keys() {
		if !strings.Contains(key.Path, "[]") {
			want[key.Path] = true
		}
	}

	got := map[string]bool{}
	for _, key := range ScalarKeys() {
		got[key.Path] = true
		if strings.Contains(key.Path, "[]") {
			t.Errorf("顶层标量里不该有带下标的路径 %s", key.Path)
		}
		if key.Type == "" {
			t.Errorf("%s 的类型为空", key.Path)
		}
	}
	for path := range want {
		if !got[path] {
			t.Errorf("键表里的顶层标量 %s 没有进 ScalarPaths", path)
		}
	}
	for path := range got {
		if !want[path] {
			t.Errorf("ScalarPaths 里的 %s 不是键表里的顶层标量", path)
		}
	}
}

// TestScalarsReadFileAsWritten 标量的取值按文件原样读：写了的报取值，没写的报 nil，
// 不是填过默认值之后的取值。
func TestScalarsReadFileAsWritten(t *testing.T) {
	path := writeConfig(t, `listen: 127.0.0.1:9999
log:
  level: debug

channels:
  - name: example
    dialect: openai
    base_url: "https://api.example.com/v1"
    path: /chat/completions
    auth: {style: none}

models:
  - {match: "gpt-*", channel: example}
`)
	env, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	scalars, err := env.Scalars()
	if err != nil {
		t.Fatalf("Scalars: %v", err)
	}

	byPath := map[string]Scalar{}
	for _, scalar := range scalars {
		byPath[scalar.Path] = scalar
	}

	written := map[string]any{
		"listen":    "127.0.0.1:9999",
		"log.level": "debug",
	}
	for path, want := range written {
		if got := byPath[path].Written; got != want {
			t.Errorf("%s 写下的取值是 %v，期望 %v", path, got, want)
		}
		if byPath[path].Defaulted() {
			t.Errorf("%s 写在文件里了，不该报默认值", path)
		}
	}

	// 文件里没写的项按 nil 报，呈现时退回键表里的默认值说明；log.format 与
	// retry.in_channel 这种可空字段不因为 core 会兜底就报成「写了」。
	for _, path := range []string{"log.format", "plugins.dir", "retry.in_channel", "defaults.max_body_bytes", "affinity.ttl"} {
		if byPath[path].Written != nil {
			t.Errorf("%s 没写在文件里，不该报取值 %v", path, byPath[path].Written)
		}
		if !byPath[path].Defaulted() {
			t.Errorf("%s 没写在文件里，应当报默认值", path)
		}
	}
	if got := byPath["retry.in_channel"].Text(); got != "408,429,500,502,503,504" {
		t.Errorf("没写时应当给出键表里的默认值说明，实际 %q", got)
	}

	// 相对路径按配置目录归一：这是生效取值，与 config get 同一套口径。
	if got := byPath["plugins.dir"].Resolved; got != filepath.Join(filepath.Dir(path), "plugins") {
		t.Errorf("plugins.dir 的生效取值是 %v，期望配置目录下的 plugins", got)
	}
}

// TestDefaultScalarsAreCoreDefaults 骨架写出的标量是 core 的默认值：只有 core 的
// Config.Defaults 填过的键才出现，可空字段与没默认值的字段都不出现。
func TestDefaultScalarsAreCoreDefaults(t *testing.T) {
	scalars, err := DefaultScalars()
	if err != nil {
		t.Fatalf("DefaultScalars: %v", err)
	}

	got := map[string]any{}
	for _, scalar := range scalars {
		got[scalar.Path] = scalar.Written
	}
	want := map[string]any{
		"listen":      config.DefaultListen,
		"log.format":  config.DefaultLogFormat,
		"log.level":   config.DefaultLogLevel,
		"plugins.dir": config.DefaultPluginDir,
	}
	for path, value := range want {
		if got[path] != value {
			t.Errorf("%s 的默认值是 %v，期望 %v", path, got[path], value)
		}
	}
	// 这几个的默认值不由 core 的 Defaults 填（可空字段，或压根没有默认值），骨架不写。
	for _, path := range []string{"defaults.max_body_bytes", "affinity.ttl", "retry.in_channel", "retry.failover"} {
		if _, ok := got[path]; ok {
			t.Errorf("%s 不该出现在骨架里，core 的 Defaults 不填它", path)
		}
	}
}

// TestTemplateIsValid 骨架本身能过校验，并且在顶层标量之外带上示例段。
func TestTemplateIsValid(t *testing.T) {
	source, err := Template("凭据只写环境变量名。", `channels:
  - name: example
    dialect: openai
    base_url: "https://api.example.com/v1"
    path: /chat/completions
    auth: {style: bearer, key_env: EXAMPLE_API_KEY}

models:
  - {match: "gpt-*", channel: example}

plugins:
  list: []
`)
	if err != nil {
		t.Fatalf("Template: %v", err)
	}
	text := string(source)
	if !strings.HasPrefix(text, "# 凭据只写环境变量名。\n") {
		t.Errorf("骨架顶上应当是给定的注释:\n%s", text)
	}
	for _, want := range []string{"listen: 127.0.0.1:8787", "dir: plugins", "list: []", "name: example", "match: \"gpt-*\""} {
		if !strings.Contains(text, want) {
			t.Errorf("骨架里没有 %q:\n%s", want, text)
		}
	}

	path := writeConfig(t, text)
	if _, err := Load(path); err != nil {
		t.Fatalf("骨架应当能加载: %v\n%s", err, text)
	}
}

// TestOtherScalarPathsAreTheRest 白名单之外的顶层标量清单是键表减掉白名单。
func TestOtherScalarPathsAreTheRest(t *testing.T) {
	rest := OtherScalarPaths()
	allowed := map[string]bool{}
	for _, path := range SettablePaths {
		allowed[path] = true
	}
	for _, path := range rest {
		if allowed[path] {
			t.Errorf("白名单里的 %s 不该出现在「其余顶层标量」里", path)
		}
	}
	if len(rest)+len(SettablePaths) != len(ScalarPaths()) {
		t.Errorf("两份清单加起来是 %d 项，键表里有 %d 项顶层标量",
			len(rest)+len(SettablePaths), len(ScalarPaths()))
	}
}

// TestEncodeValueByKeyType 整数按键表里的类型编码，落盘的是数字不是字符串。
func TestEncodeValueByKeyType(t *testing.T) {
	value, err := EncodeValue("defaults.max_body_bytes", "1048576")
	if err != nil {
		t.Fatalf("EncodeValue: %v", err)
	}
	if number, ok := value.(int); !ok || number != 1048576 {
		t.Errorf("整数键编码成 %T(%v)，期望 int", value, value)
	}

	// 字符串与时长原样交给 core：Duration 自带 UnmarshalYAML。
	if value, err := EncodeValue("listen", "127.0.0.1:1"); err != nil || value != "127.0.0.1:1" {
		t.Errorf("字符串键编码结果是 %v（%v）", value, err)
	}
	if value, err := EncodeValue("affinity.ttl", "5m"); err != nil || value != "5m" {
		t.Errorf("时长键编码结果是 %v（%v）", value, err)
	}

	// 不在键表里的路径按字符串走，由 core 的校验兜底。
	if value, err := EncodeValue("channels.0.timeout", "1m"); err != nil || value != "1m" {
		t.Errorf("表外路径的编码结果是 %v（%v）", value, err)
	}

	if _, err := EncodeValue("defaults.max_body_bytes", "1MiB"); err == nil {
		t.Error("整数键给非整数应当报错")
	}
}

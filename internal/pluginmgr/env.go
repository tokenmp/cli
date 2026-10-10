// Package pluginmgr 把插件目录、配置文件与 core 的插件宿主拼在一起，供插件管理命令
// 使用。装配只发生在这里，命令层只负责取值与输出。
package pluginmgr

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tokenmp/core/config"
	"github.com/tokenmp/core/plugin"

	"github.com/tokenmp/cli/internal/configedit"
	"github.com/tokenmp/cli/internal/configmgr"
)

// 配置 plugins.list 里可以改写的字段名。
const (
	FieldEnabled = "enabled"
	FieldOrder   = "order"
)

// Env 是一次插件管理命令用到的上下文。
type Env struct {
	// 配置的加载与路径处理由 configmgr 负责，这里只补上插件目录与日志。
	*configmgr.Env
	// Dir 是解析后的插件目录，可能不存在。
	Dir string
	// Logger 接收插件通过 host.log 写出的日志。管理命令只关心结果，默认丢弃。
	Logger *slog.Logger
}

// Load 加载配置。配置文件不存在不算错误：只看插件目录的命令不必先有配置。
func Load(configPath, dirOverride string) (*Env, error) {
	base, err := configmgr.Open(configPath)
	if err != nil {
		return nil, err
	}

	env := &Env{
		Env:    base,
		Dir:    base.Config.Plugins.Dir,
		Logger: slog.New(slog.DiscardHandler),
	}
	if dirOverride != "" {
		env.Dir = dirOverride
	}
	return env, nil
}

// Exists 报告插件目录是否存在。
func (e *Env) Exists() bool {
	info, err := os.Stat(e.Dir)
	return err == nil && info.IsDir()
}

// File 是插件目录里的一个候选插件文件。
type File struct {
	// Key 是从文件位置推出的 key，检查时与 meta.key 比对。
	Key string
	// Path 是插件文件路径。
	Path string
}

// Files 列出候选插件文件：<dir>/<key>/plugin.js 与平铺的 <dir>/<key>.js。
//
// 这里不编译插件，语法出错的插件因此也能出现在结果里。目录不存在时返回空列表。
func (e *Env) Files() ([]File, error) {
	entries, err := os.ReadDir(e.Dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("读取插件目录 %s 失败: %w", e.Dir, err)
	}

	var files []File
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			candidate := filepath.Join(e.Dir, name, plugin.PluginFile)
			if _, err := os.Stat(candidate); err == nil {
				files = append(files, File{Key: name, Path: candidate})
			}
			continue
		}
		if strings.HasSuffix(name, ".js") {
			files = append(files, File{Key: strings.TrimSuffix(name, ".js"), Path: filepath.Join(e.Dir, name)})
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

// Broken 是编译失败的插件文件。
type Broken struct {
	// File 是插件文件。
	File File
	// Err 是失败原因。
	Err error
}

// Discover 编译插件目录里的全部插件，把编译成功与失败分开返回。
//
// 单个插件编译失败不影响其余的：列表与检查都要把坏插件连同原因一起报出来，而不是
// 整次扫描失败。
func (e *Env) Discover() ([]*plugin.Loaded, []Broken, error) {
	files, err := e.Files()
	if err != nil {
		return nil, nil, err
	}

	loaded := make([]*plugin.Loaded, 0, len(files))
	var broken []Broken
	for _, file := range files {
		item, err := plugin.LoadFile(file.Path, plugin.DefaultLimits())
		if err != nil {
			broken = append(broken, Broken{File: file, Err: err})
			continue
		}
		loaded = append(loaded, item)
	}
	return loaded, broken, nil
}

// Find 编译并返回一个插件。目录里没有这个 key 时报错，并在错误里列出可用的 key。
//
// 只编译指定的那个文件，因此目录里其它插件坏了也不会影响这个命令。
func (e *Env) Find(key string) (*plugin.Loaded, error) {
	files, err := e.Files()
	if err != nil {
		return nil, err
	}

	available := make([]string, 0, len(files))
	for _, file := range files {
		if file.Key == key {
			return plugin.LoadFile(file.Path, plugin.DefaultLimits())
		}
		available = append(available, file.Key)
	}
	if len(available) == 0 {
		return nil, fmt.Errorf("插件目录 %s 里没有插件", e.Dir)
	}
	return nil, fmt.Errorf("插件目录 %s 里没有 %s，现有插件为 %s", e.Dir, key, strings.Join(available, "、"))
}

// Bind 用配置里该插件的 config 段绑定实例。
func (e *Env) Bind(loaded *plugin.Loaded) *plugin.Instance {
	return loaded.Bind(e.Config.PluginConfig(loaded.Key()))
}

// State 是一个插件在列表里的全部已知信息。
type State struct {
	// Loaded 是编译成功的插件。编译失败或目录里没有时为 nil。
	Loaded *plugin.Loaded
	// Entry 是配置里的登记项。
	Entry config.Plugin
	// Registered 报告配置里有没有这个 key。
	Registered bool
	// Err 非空表示插件文件编译失败，原因由它给出。
	Err error
	// Path 是插件文件路径。编译失败时用它指出问题所在。
	Path string
}

// Key 返回插件 key。
func (s State) Key() string {
	if s.Loaded != nil {
		return s.Loaded.Key()
	}
	if s.Entry.Key != "" {
		return s.Entry.Key
	}
	return s.Path
}

// Missing 报告配置里登记了但目录里没有。
func (s State) Missing() bool { return s.Registered && s.Loaded == nil && s.Err == nil }

// Broken 报告插件文件编译失败。
func (s State) Broken() bool { return s.Err != nil }

// Enabled 报告这个插件是否参与转发。
func (s State) Enabled() bool { return s.Registered && s.Entry.Enabled }

// Summary 是插件的一行摘要，也是 --json 的输出形状。
type Summary struct {
	Key        string        `json:"key"`
	Name       string        `json:"name,omitempty"`
	Version    string        `json:"version,omitempty"`
	APIVersion string        `json:"apiVersion,omitempty"`
	Hooks      []plugin.Hook `json:"hooks,omitempty"`
	Path       string        `json:"path,omitempty"`
	Enabled    bool          `json:"enabled"`
	Order      int           `json:"order"`
	Registered bool          `json:"registered"`
	Missing    bool          `json:"missing,omitempty"`
	Error      string        `json:"error,omitempty"`
}

// Summary 汇总一个插件的状态。
func (s State) Summary() Summary {
	summary := Summary{
		Key:        s.Key(),
		Path:       s.Path,
		Enabled:    s.Enabled(),
		Order:      s.Entry.Order,
		Registered: s.Registered,
		Missing:    s.Missing(),
	}
	if s.Loaded != nil {
		manifest := s.Loaded.Manifest()
		summary.Name = manifest.Name
		summary.Version = manifest.Version
		summary.APIVersion = manifest.APIVersion
		summary.Hooks = manifest.Hooks
		summary.Path = s.Loaded.Path()
	}
	if s.Err != nil {
		summary.Error = s.Err.Error()
	}
	return summary
}

// States 返回插件目录与配置合起来看到的状态，顺序与转发时的执行顺序一致：启用中的
// 按 order 升序在前，编译失败的其次，目录里有但没登记的再次，停用的最后。
func (e *Env) States() ([]State, error) {
	loaded, broken, err := e.Discover()
	if err != nil {
		return nil, err
	}
	available := make(map[string]*plugin.Loaded, len(loaded))
	for _, item := range loaded {
		available[item.Key()] = item
	}
	failed := make(map[string]Broken, len(broken))
	for _, item := range broken {
		failed[item.File.Key] = item
	}

	states := make([]State, 0, len(loaded)+len(broken)+len(e.Config.Plugins.List))
	seen := make(map[string]bool, len(e.Config.Plugins.List))
	for _, entry := range e.Config.Plugins.List {
		state := State{Entry: entry, Registered: true, Loaded: available[entry.Key]}
		if item, ok := failed[entry.Key]; ok {
			state.Err, state.Path = item.Err, item.File.Path
		}
		states = append(states, state)
		seen[entry.Key] = true
	}
	for _, item := range loaded {
		if seen[item.Key()] {
			continue
		}
		states = append(states, State{Loaded: item})
	}
	for _, item := range broken {
		if seen[item.File.Key] {
			continue
		}
		states = append(states, State{Entry: config.Plugin{Key: item.File.Key}, Err: item.Err, Path: item.File.Path})
	}

	sort.SliceStable(states, func(i, j int) bool {
		left, right := rank(states[i]), rank(states[j])
		if left != right {
			return left < right
		}
		if left == rankEnabled && states[i].Entry.Order != states[j].Entry.Order {
			return states[i].Entry.Order < states[j].Entry.Order
		}
		return states[i].Key() < states[j].Key()
	})
	return states, nil
}

// rank 是状态分组：启用中的在最前，编译失败的其次，未登记的再次，停用的最后。
const (
	rankEnabled = iota
	rankBroken
	rankUnregistered
	rankDisabled
)

func rank(state State) int {
	switch {
	case state.Broken():
		return rankBroken
	case state.Enabled():
		return rankEnabled
	case !state.Registered:
		return rankUnregistered
	default:
		return rankDisabled
	}
}

// Edit 改写配置里某个插件条目的字段，写回后重新加载校验；校验失败时还原原文件。
func (e *Env) Edit(key, field string, value any) error {
	if !e.HasConfig {
		return fmt.Errorf("配置文件 %s 不存在，无法改写启用状态与顺序", e.ConfigPath)
	}
	return configedit.SetPluginField(e.ConfigPath, key, field, value, func() error {
		_, err := config.Load(e.ConfigPath)
		return err
	})
}

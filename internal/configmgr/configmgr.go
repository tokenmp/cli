// Package configmgr 加载配置文件，并把配置还原成与文件同形的通用值，供 config 命令
// 取值、改写与输出。装配只发生在这里，命令层只负责取值与呈现。
package configmgr

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tokenmp/core/config"
	"gopkg.in/yaml.v3"

	"github.com/tokenmp/cli/internal/configedit"
)

// SettablePaths 是配置里可能还没有、但允许补上的标量路径。已经存在的标量都能改，
// 这几个只是白名单里额外的项。
var SettablePaths = []string{"listen", "log.format", "log.level", "plugins.dir"}

// Env 是一次 config 命令用到的上下文。
type Env struct {
	// ConfigPath 是配置文件的绝对路径。
	ConfigPath string
	// Config 是加载后的配置。配置文件不存在时是只剩默认值的空配置。
	Config *config.Config
	// HasConfig 报告配置文件是否存在。
	HasConfig bool
	// Dir 是配置文件所在目录，插件目录等相对路径以它为基准。
	Dir string
}

// Open 读取并校验配置。配置文件不存在不算错误：只看目录的命令不必先有配置。
func Open(configPath string) (*Env, error) {
	cfg, hasConfig, err := Inspect(configPath)
	if err != nil {
		return nil, err
	}
	absolute, err := filepath.Abs(configPath)
	if err != nil {
		return nil, fmt.Errorf("解析配置文件路径失败: %w", err)
	}
	// 配置文件不存在时 core 没机会归一相对路径，这里补上，让输出的插件目录与
	// 有配置文件时一致。
	if !hasConfig && !filepath.IsAbs(cfg.Plugins.Dir) {
		cfg.Plugins.Dir = filepath.Join(filepath.Dir(absolute), cfg.Plugins.Dir)
	}
	return &Env{
		ConfigPath: absolute,
		Config:     cfg,
		HasConfig:  hasConfig,
		Dir:        filepath.Dir(absolute),
	}, nil
}

// Load 读取并校验配置，配置文件不存在时报错。
func Load(configPath string) (*Env, error) {
	env, err := Open(configPath)
	if err != nil {
		return nil, err
	}
	if !env.HasConfig {
		return nil, fmt.Errorf("配置文件 %s 不存在，用 tokenmp config init 生成一份", configPath)
	}
	return env, nil
}

// Inspect 读取并校验配置，把校验错误原样返回，让调用方决定怎么呈现。
//
// 第二个返回值只回答「文件在不在」：文件在但不合法时它仍为真，错误由第三个返回值
// 给出。配置文件不存在时返回一份只剩默认值的空配置与假值，不算错误。
func Inspect(configPath string) (*config.Config, bool, error) {
	cfg, err := config.Load(configPath)
	switch {
	case err == nil:
		return cfg, true, nil
	case errors.Is(err, fs.ErrNotExist):
		empty := &config.Config{}
		empty.Defaults()
		return empty, false, nil
	default:
		return nil, true, err
	}
}

// Generic 把配置还原成与配置文件同形的通用值，字段名沿用 yaml 标签。
//
// core 的配置结构体只有 yaml 标签，直接编码成 JSON 会得到 Go 字段名，因此先过一遍
// YAML：这样 --json 输出与 config get 的路径都与配置文件一一对应。
func (e *Env) Generic() (map[string]any, error) {
	encoded, err := yaml.Marshal(e.Config)
	if err != nil {
		return nil, fmt.Errorf("编码配置失败: %w", err)
	}
	var generic map[string]any
	if err := yaml.Unmarshal(encoded, &generic); err != nil {
		return nil, fmt.Errorf("解析配置失败: %w", err)
	}
	return generic, nil
}

// Pruned 返回去掉空值的配置视图，字段顺序与配置文件一致。
//
// 生效配置里的默认值保留，空字符串、空段落、空列表与 null 不出现：它们只表示
// 「没写」，没有信息量。--json 输出用这个形状。
func (e *Env) Pruned() (map[string]any, error) {
	encoded, err := e.PrunedYAML()
	if err != nil {
		return nil, err
	}
	var generic map[string]any
	if err := yaml.Unmarshal(encoded, &generic); err != nil {
		return nil, fmt.Errorf("解析配置失败: %w", err)
	}
	return generic, nil
}

// PrunedYAML 把生效配置编码成两格缩进的 YAML，并去掉空值字段。
//
// 先编码成节点再删空值，而不是先转成 map：节点保留了结构体的字段顺序，转 map 之后
// 字段会按名称排序。
func (e *Env) PrunedYAML() ([]byte, error) {
	var document yaml.Node
	if err := document.Encode(e.Config); err != nil {
		return nil, fmt.Errorf("编码配置失败: %w", err)
	}
	// Node.Encode 把节点本身设成值对应的节点，不包一层文档节点；保险起见两种情况
	// 都处理。
	node := &document
	if document.Kind == yaml.DocumentNode && len(document.Content) > 0 {
		node = document.Content[0]
	}
	pruneNode(node)

	var buffer bytes.Buffer
	encoder := yaml.NewEncoder(&buffer)
	encoder.SetIndent(2)
	if err := encoder.Encode(&document); err != nil {
		return nil, fmt.Errorf("编码配置失败: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return nil, fmt.Errorf("编码配置失败: %w", err)
	}
	return buffer.Bytes(), nil
}

// pruneNode 递归删掉映射里的空值项。
//
// 只删空字符串、空段落、空列表与 null。false 与 0 都留着：它们是插件启用状态与重试
// 次数这类字段的真实取值，删掉会让人以为没配。
func pruneNode(node *yaml.Node) {
	switch node.Kind {
	case yaml.MappingNode:
		kept := make([]*yaml.Node, 0, len(node.Content))
		for i := 0; i+1 < len(node.Content); i += 2 {
			key, value := node.Content[i], node.Content[i+1]
			pruneNode(value)
			if isEmptyValue(value) {
				continue
			}
			kept = append(kept, key, value)
		}
		node.Content = kept
	case yaml.SequenceNode:
		for _, item := range node.Content {
			pruneNode(item)
		}
	}
}

// isEmptyValue 报告节点是否是空值。
//
// 标量按取值判断：空字符串与 null 的值都是空串，而 false、0 这些都有实际取值。
func isEmptyValue(node *yaml.Node) bool {
	switch node.Kind {
	case yaml.ScalarNode:
		return node.Value == ""
	case yaml.MappingNode, yaml.SequenceNode:
		return len(node.Content) == 0
	default:
		return false
	}
}

// Value 按点分路径取值，数字段表示数组下标。
func (e *Env) Value(path string) (any, error) {
	generic, err := e.Generic()
	if err != nil {
		return nil, err
	}

	current := any(generic)
	for _, segment := range strings.Split(path, ".") {
		switch node := current.(type) {
		case map[string]any:
			next, ok := node[segment]
			if !ok {
				return nil, fmt.Errorf("配置里没有 %s", path)
			}
			current = next
		case []any:
			index, err := strconv.Atoi(segment)
			if err != nil {
				return nil, fmt.Errorf("路径 %s 的 %s 段不是数组下标", path, segment)
			}
			if index < 0 || index >= len(node) {
				return nil, fmt.Errorf("路径 %s 的下标 %d 越界", path, index)
			}
			current = node[index]
		default:
			return nil, fmt.Errorf("路径 %s 的 %s 段取不到值", path, segment)
		}
	}
	return current, nil
}

// Set 改写一个标量字段，写回后重新加载校验；校验失败时还原原文件。
//
// 只接受已经存在的标量路径与 SettablePaths 里的路径。指向段落或列表的路径会被拒绝：
// 整体替换段落几乎总是笔误，而且会连带删掉里面的注释。
func (e *Env) Set(path, value string) error {
	if !e.HasConfig {
		return fmt.Errorf("配置文件 %s 不存在，用 tokenmp config init 生成一份", e.ConfigPath)
	}
	if err := e.checkSettable(path); err != nil {
		return err
	}

	return configedit.SetScalar(e.ConfigPath, strings.Split(path, "."), value, func() error {
		_, err := config.Load(e.ConfigPath)
		return err
	})
}

// checkSettable 确认路径可改。
func (e *Env) checkSettable(path string) error {
	value, err := e.Value(path)
	if err == nil {
		switch value.(type) {
		case map[string]any:
			return fmt.Errorf("%s 是一个段落，不能整体改写", path)
		case []any:
			return fmt.Errorf("%s 是一个列表，不能整体改写", path)
		default:
			return nil
		}
	}
	for _, allowed := range SettablePaths {
		if path == allowed {
			return nil
		}
	}
	return fmt.Errorf("配置里没有 %s，可改写的路径是 %s，以及任何已经存在的标量字段",
		path, strings.Join(SettablePaths, "、"))
}

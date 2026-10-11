// Package configmgr 加载配置文件，并把配置还原成与文件同形的通用值，供 config 命令
// 取值、改写与输出。装配只发生在这里，命令层只负责取值与呈现。
//
// 配置有两个形态：按文件原样解析的那份保留顶层 accounts 与渠道的 auth.accounts，
// 名字与下标与配置文件一一对应；展开之后的那份没有账号层，渠道名变成运行时用的
// pool/team-a 这种写法。写回走 configedit，是在文件的 YAML 节点上按路径与下标定点
// 改写，因此凡是要拿名字或下标写回的命令都读按文件原样解析的那份。
package configmgr

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
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
//
// 除非用的是 LoadExpanded，Config 都是按文件原样解析的那份：顶层 accounts 还在，
// 渠道名与 models 的下标与配置文件一一对应。
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
//
// 返回的是按文件原样解析的形态，顶层账号没有展开。要反映运行时形态的命令用
// LoadExpanded。
func Open(configPath string) (*Env, error) {
	return openWith(configPath, Inspect)
}

// Load 读取并校验配置，配置文件不存在时报错。
//
// 返回的是按文件原样解析的形态：需要按名字或下标写回配置的命令用它，位置才与
// 文件里的写法一致。
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

// LoadAccounts 与 Load 相同，只在读入时放行「顶层账号没有被任何渠道引用」这一条。
//
// 顶层账号是「渠道按名字引用」的加载期形态，于是新建一个账号到它被渠道引用之间、
// 以及账号刚被解除引用还没删掉时，配置都会处于「账号没人引用」的中间状态。这个状态
// 下 core 的 config.Load 会拒绝，但文件本身是好的，accounts 命令组的读入与写回校验
// 用它就能继续管理这份配置；channels create 也用它读入，好让新建的账号能立刻接上引用。
//
// 约束仍在别处把关：config validate 与 command run / test 用严格校验，服务不会带着
// 没人引用的账号启动；写回校验见 accountsReloadableCheck，除这一条外的问题照常报错。
func LoadAccounts(configPath string) (*Env, error) {
	env, err := openWith(configPath, inspectManaging)
	if err != nil {
		return nil, err
	}
	if !env.HasConfig {
		return nil, fmt.Errorf("配置文件 %s 不存在，用 tokenmp config init 生成一份", configPath)
	}
	return env, nil
}

// LoadExpanded 读取并校验配置，返回展开顶层账号之后的形态。
//
// 只有反映运行时的命令用它：run 与 test 按展开后的渠道装配服务，models list 的
// 候选试算也按展开后的规则，那才是服务实际会用的候选。其余命令按文件原样读，见 Load。
func LoadExpanded(configPath string) (*Env, error) {
	env, err := openWith(configPath, inspectExpanded)
	if err != nil {
		return nil, err
	}
	if !env.HasConfig {
		return nil, fmt.Errorf("配置文件 %s 不存在，用 tokenmp config init 生成一份", configPath)
	}
	return env, nil
}

// openWith 走完「读配置、定位目录、补相对路径」的公共流程。inspect 决定读哪一份形态。
func openWith(configPath string, inspect func(string) (*config.Config, bool, error)) (*Env, error) {
	cfg, hasConfig, err := inspect(configPath)
	if err != nil {
		return nil, err
	}
	absolute, err := filepath.Abs(configPath)
	if err != nil {
		return nil, fmt.Errorf("解析配置文件路径失败: %w", err)
	}
	// 配置文件不存在时解析器没机会归一相对路径，这里补上，让输出的插件目录与
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

// Inspect 读取并校验配置，把校验错误原样返回，让调用方决定怎么呈现。
//
// 第二个返回值只回答「文件在不在」：文件在但不合法时它仍为真，错误由第三个返回值
// 给出。配置文件不存在时返回一份只剩默认值的空配置与假值，不算错误。
//
// 返回的是按文件原样解析的形态，路径与下标口径与 config get 一致。
func Inspect(configPath string) (*config.Config, bool, error) {
	return inspectWith(configPath, false)
}

// inspectManaging 与 Inspect 同理，放行「顶层账号没有被任何渠道引用」这一条，
// 供 LoadAccounts 使用。
func inspectManaging(configPath string) (*config.Config, bool, error) {
	return inspectWith(configPath, true)
}

// inspectWith 走完「按文件原样解析、判断文件在不在」的公共流程。
// tolerateUnreferenced 决定是否放行没人引用的顶层账号。
func inspectWith(configPath string, tolerateUnreferenced bool) (*config.Config, bool, error) {
	cfg, err := parseConfigFileWith(configPath, tolerateUnreferenced)
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

// inspectExpanded 与 Inspect 同理，只是在返回之前按 core 的 Parse 展开顶层账号。
func inspectExpanded(configPath string) (*config.Config, bool, error) {
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

// parseConfigFileWith 按配置文件里的原样解析：顶层 accounts 与渠道的 auth.accounts 都
// 留在返回值里，不做 Expand。
//
// core 的 config.Parse 在返回之前自己会展开一次，因此这个形态只能这边自己拼：解码开启
// KnownFields，填默认值、校验、补绝对路径，再把 plugins.dir 这类相对路径按配置目录
// 归一。最后一步与 core 的 resolvePaths 同一套规则，那份没有导出，这里照写。
//
// 展开之后的形态也校验一次：展开名与手写渠道名撞车这类问题只在平铺形态里出现，
// 少这一步会漏掉；core 的 Parse 同样校验两次。
//
// tolerateUnreferenced 为真时在动手校验之前把没人引用的账号从副本里去掉，从而放行
// core 的「顶层账号没有被任何渠道引用」。账号条目本身仍然按文件原样返回，写回时也
// 不会少写。被去掉的只是没人引用的账号：它们自己的字段校验在这里略过，由账号命令在
// 写入前把关；被引用的账号照旧参与校验。
func parseConfigFileWith(path string, tolerateUnreferenced bool) (*config.Config, error) {
	//nolint:gosec // 路径来自调用方：配置文件位置由使用者给出。
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	var cfg config.Config
	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("配置文件为空")
		}
		return nil, err
	}
	cfg.Defaults()
	checked := &cfg
	if tolerateUnreferenced {
		pruned := cfg
		pruned.Accounts = referencedAccounts(cfg)
		checked = &pruned
	}
	if err := checked.Validate(); err != nil {
		return nil, err
	}
	if err := checked.Expand().Validate(); err != nil {
		return nil, err
	}

	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("解析配置文件路径失败: %w", err)
	}
	cfg.Path = absolute
	if cfg.Plugins.Dir != "" && !filepath.IsAbs(cfg.Plugins.Dir) {
		cfg.Plugins.Dir = filepath.Join(filepath.Dir(absolute), cfg.Plugins.Dir)
	}
	return &cfg, nil
}

// referencedAccounts 只留下被至少一条渠道引用的账号，供 parseConfigFileWith 的
// 宽容校验使用。顺序按文件里的先后。
func referencedAccounts(cfg config.Config) []config.Account {
	referenced := make(map[string]bool, len(cfg.Accounts))
	for _, channel := range cfg.Channels {
		for _, name := range channel.Auth.Accounts {
			referenced[name] = true
		}
	}
	kept := make([]config.Account, 0, len(cfg.Accounts))
	for _, account := range cfg.Accounts {
		if referenced[account.Name] {
			kept = append(kept, account)
		}
	}
	return kept
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
// 加载后的默认值保留，空字符串、空段落、空列表与 null 不出现：它们只表示
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

// PrunedYAML 把加载后的配置编码成两格缩进的 YAML，并去掉空值字段。
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
// 路径按文件原样解析的那份配置判断存不存在，与 config get 同一套口径。
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

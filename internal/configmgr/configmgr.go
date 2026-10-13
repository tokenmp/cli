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

	"github.com/tokenmp/cli/internal/billing"
	"github.com/tokenmp/cli/internal/configedit"
	"github.com/tokenmp/cli/internal/records"
)

// SettablePaths 是配置里可能还没有、但允许补上的标量路径。已经存在的标量都能改，
// 这几个只是白名单里额外的项。
//
// 它不由 core 的键表生成，键表里没有「是否可 set」这一列：那是 config set 的策略，
// 不是配置本身的事实，钉进 core 的公开面等于把一条命令行的策略写进库的兼容承诺。
// 这份名单由 CLI 自己维护，TestSettablePathsAreKnownKeys 盯着它是 config.Keys() 路径
// 集合的子集，避免白名单里留下键表里已经没有的路径。
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
	// Records 是 records 段，整段没写时为 nil。它不是 core 的配置字段，由这层接住，
	// 见 fileConfig。
	Records *records.Config
	// Cost 是 cost 段，整段没写时为 nil。同样不是 core 的字段，由这层接住。
	Cost *billing.Config
	// HasConfig 报告配置文件是否存在。
	HasConfig bool
	// Dir 是配置文件所在目录，插件目录等相对路径以它为基准。
	Dir string
}

// fileConfig 在 core 的配置模型之外多带 cli 自己的 records 与 cost 段。
//
// 两段都是 cli 侧的概念：records 把每次调用推给 web 的摄入端点，cost 是付给上游的
// 成本单价，core 都不认识。而解码开着 KnownFields，直接把文件解进 core.Config 会因为
// 多出这两个键而报错，因此用一层内联包装把它们接住，core 的那部分照旧走 config.Config
// 的方法与校验。
//
// 两段都用指针：nil 表示整段没写，config show 里也就不出现；写了才出现，并带上填好的
// 默认值。
type fileConfig struct {
	config.Config `yaml:",inline"`
	Records       *records.Config `yaml:"records,omitempty"`
	Cost          *billing.Config `yaml:"cost,omitempty"`
}

// CliSections 是 core 配置模型之外、cli 自己那几段配置。整段没写时对应字段为 nil。
type CliSections struct {
	Records *records.Config
	Cost    *billing.Config
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
func openWith(configPath string, inspect func(string) (*config.Config, CliSections, bool, error)) (*Env, error) {
	cfg, sections, hasConfig, err := inspect(configPath)
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
		Records:    sections.Records,
		Cost:       sections.Cost,
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
func Inspect(configPath string) (*config.Config, CliSections, bool, error) {
	cfg, sections, err := parseConfigFile(configPath)
	switch {
	case err == nil:
		return cfg, sections, true, nil
	case errors.Is(err, fs.ErrNotExist):
		empty := &config.Config{}
		empty.Defaults()
		return empty, CliSections{}, false, nil
	default:
		return nil, CliSections{}, true, err
	}
}

// inspectExpanded 与 Inspect 同理，只是在返回之前展开顶层账号。
//
// 它不走 core 的 config.Load：那份的解码认不出 records 段。这里复用 parseConfigFile
// 自己拼的那一份，再调一次 Expand，结果与 core.Load 一致，差异由 configmgr 的
// 防回归测试盯着。
func inspectExpanded(configPath string) (*config.Config, CliSections, bool, error) {
	cfg, sections, err := parseConfigFile(configPath)
	switch {
	case err == nil:
		return cfg.Expand(), sections, true, nil
	case errors.Is(err, fs.ErrNotExist):
		empty := &config.Config{}
		empty.Defaults()
		return empty, CliSections{}, false, nil
	default:
		return nil, CliSections{}, true, err
	}
}

// Validate 重新读取并校验配置文件，供写回之后的校验用。
//
// 它认得 cli 自己的 records 段，因此不能用 core 的 config.Load 代替：那份的解码会把
// records 当成拼错的字段。
func Validate(path string) error {
	_, _, err := parseConfigFile(path)
	return err
}

// parseConfigFile 按配置文件里的原样解析：顶层 accounts 与渠道的 auth.accounts 都
// 留在返回值里，不做 Expand。
//
// core 的 config.Parse 在返回之前自己会展开一次，因此这个形态只能这边自己拼：解码开启
// KnownFields，填默认值、校验、补绝对路径，再把 plugins.dir 这类相对路径按配置目录
// 归一。最后一步与 core 的 resolvePaths 同一套规则，那份没有导出，这里照写。
//
// 展开之后的形态也校验一次：展开名与手写渠道名撞车这类问题只在平铺形态里出现，
// 少这一步会漏掉；core 的 Parse 同样校验两次。
func parseConfigFile(path string) (*config.Config, CliSections, error) {
	//nolint:gosec // 路径来自调用方：配置文件位置由使用者给出。
	file, err := os.Open(path)
	if err != nil {
		return nil, CliSections{}, err
	}
	defer func() { _ = file.Close() }()

	var raw fileConfig
	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	if err := decoder.Decode(&raw); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, CliSections{}, errors.New("配置文件为空")
		}
		return nil, CliSections{}, err
	}
	cfg := &raw.Config
	cfg.Defaults()
	if err := cfg.Validate(); err != nil {
		return nil, CliSections{}, err
	}
	if raw.Records != nil {
		raw.Records.Defaults()
		if err := raw.Records.Validate(); err != nil {
			return nil, CliSections{}, err
		}
	}
	if raw.Cost != nil {
		raw.Cost.Defaults()
		if err := raw.Cost.Validate(); err != nil {
			return nil, CliSections{}, err
		}
	}
	if err := cfg.Expand().Validate(); err != nil {
		return nil, CliSections{}, err
	}

	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, CliSections{}, fmt.Errorf("解析配置文件路径失败: %w", err)
	}
	cfg.Path = absolute
	if cfg.Plugins.Dir != "" && !filepath.IsAbs(cfg.Plugins.Dir) {
		cfg.Plugins.Dir = filepath.Join(filepath.Dir(absolute), cfg.Plugins.Dir)
	}
	return cfg, CliSections{Records: raw.Records, Cost: raw.Cost}, nil
}

// document 把 core 的配置与 cli 自己的 records、cost 段拼回与配置文件同形的值。
func (e *Env) document() fileConfig {
	document := fileConfig{Records: e.Records, Cost: e.Cost}
	if e.Config != nil {
		document.Config = *e.Config
	}
	return document
}

// Generic 把配置还原成与配置文件同形的通用值，字段名沿用 yaml 标签。
//
// core 的配置结构体只有 yaml 标签，直接编码成 JSON 会得到 Go 字段名，因此先过一遍
// YAML：这样 --json 输出与 config get 的路径都与配置文件一一对应。
func (e *Env) Generic() (map[string]any, error) {
	encoded, err := yaml.Marshal(e.document())
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
	if err := document.Encode(e.document()); err != nil {
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
//
// 取值按 core 键表里的类型编码之后再写回：整数字段拿到 "1048576" 时写成数字而不是
// 字符串，否则重新加载会因为类型对不上而失败。
func (e *Env) Set(path, value string) error {
	if !e.HasConfig {
		return fmt.Errorf("配置文件 %s 不存在，用 tokenmp config init 生成一份", e.ConfigPath)
	}
	if err := e.checkSettable(path); err != nil {
		return err
	}
	encoded, err := EncodeValue(path, value)
	if err != nil {
		return err
	}

	return configedit.SetScalar(e.ConfigPath, strings.Split(path, "."), encoded, func() error {
		return Validate(e.ConfigPath)
	})
}

// checkSettable 确认路径可改。
//
// 能改的是「取得到值、且不是段落或列表」的路径。core 键表里的顶层标量全都在此列：它们
// 的父段落即使没写在文件里也在结构树里，因此文件里没写也能直接补上。白名单是这一条之外
// 的兜底：万一某个键连父段落都取不到，白名单里的那几个仍然放行。
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
	return fmt.Errorf("配置里没有 %s，可改写的路径是 %s 这些顶层标量，以及任何已经存在的标量字段",
		path, strings.Join(ScalarPaths(), "、"))
}

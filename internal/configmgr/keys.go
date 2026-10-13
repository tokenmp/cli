package configmgr

import (
	"bytes"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"

	"github.com/tokenmp/core/config"
	"gopkg.in/yaml.v3"
)

// Scalar 是配置里的一条顶层标量：core 键表里的那一条，以及它在这份配置里的取值。
//
// 顶层标量是路径里不含 [] 的标量键：listen、log.format、defaults.max_body_bytes、
// retry.in_channel 这些整份配置只取一个值的键。channels[].timeout 那种随条目走的键
// 不在其中 —— 它是每条渠道各自的参数，不是「这份配置」的一个值，config set 也写不
// 进去（写回只走映射，见 Set）。
//
// 帮助文案、config init 的骨架与 config path 的输出都从这里取，因此 core 的配置加减
// 一个顶层标量时它们跟着变，同一份字段清单不必各抄一遍。
type Scalar struct {
	config.Key
	// Written 是配置文件里写下的取值，没写这一项时是 nil。
	Written any
	// Resolved 是填过默认值之后的取值，core 没有默认值且文件里也没写时是 nil。
	// 与 Written 的差别只出现在相对路径上：plugins.dir 写在文件里是 plugins，
	// 这里已经按配置目录归一成绝对路径。
	Resolved any
}

// Text 返回给人看的取值：生效取值是什么就给什么，没取值时退到键表里的默认值说明。
func (s Scalar) Text() string {
	if s.Resolved == nil {
		return s.Key.Default
	}
	return fmt.Sprintf("%v", s.Resolved)
}

// Defaulted 报告这一项在配置文件里没写，取值由 core 按默认值兜底。
func (s Scalar) Defaulted() bool { return s.Written == nil }

// Scalars 返回配置里的顶层标量，按 config.Keys() 的顺序。
//
// 取值按文件原样解析的那份读，与 config get 同一套口径，因此 plugins.dir 这类相对路径
// 已经按配置目录归一。文件里没写的项，Written 是 nil、Text 给键表里的默认值说明；填过
// 默认值之后仍为空的项（defaults.max_body_bytes 这类可空字段），Resolved 也是 nil。
func (e *Env) Scalars() ([]Scalar, error) {
	resolved, err := e.Generic()
	if err != nil {
		return nil, err
	}
	written, err := e.writtenValues()
	if err != nil {
		return nil, err
	}
	blank, err := zeroValues()
	if err != nil {
		return nil, err
	}
	keys := ScalarKeys()
	scalars := make([]Scalar, 0, len(keys))
	for _, key := range keys {
		value, err := lookup(resolved, key.Path)
		if err != nil {
			return nil, err
		}
		scalars = append(scalars, Scalar{
			Key:      key,
			Written:  written[key.Path],
			Resolved: blankValue(value, blank[key.Path]),
		})
	}
	return scalars, nil
}

// DefaultScalars 返回一份新配置里 core 会填上取值的顶层标量，按 config.Keys() 的顺序。
//
// 它答的是「core 自己填什么」，而不是「键表里有哪些键」：拿一份填过默认值的配置与一份
// 零值配置比，取值变了的那些就是 core 填的默认值（今天是 listen、log.format、log.level
// 与 plugins.dir）。config init 的骨架用它，因而只写这几项；其余键不写在文件里就沿用
// core 的默认值，骨架不必再抄一份键清单。
//
// 取值不经路径归一：plugins.dir 出来是 `plugins` 而不是绝对路径 —— 骨架描述的是
// 「一份新配置长什么样」，与目标路径上已有的文件无关。
func DefaultScalars() ([]Scalar, error) {
	fresh := &config.Config{}
	fresh.Defaults()

	scalars, err := (&Env{Config: fresh}).Scalars()
	if err != nil {
		return nil, err
	}
	filled := make([]Scalar, 0, len(scalars))
	for _, scalar := range scalars {
		if scalar.Resolved != nil {
			scalar.Written = scalar.Resolved
			filled = append(filled, scalar)
		}
	}
	return filled, nil
}

// Template 生成 config init 写入的配置骨架。
//
// 顶层标量由 defaultScalarsWithOverrides 给出，随后接上 sections 给的示例段：channels
// 与 models 这些要使用者自己改的部分不进 core 的键表，只能由调用方给出。示例段与标量
// 同名的那一段会并进去（标量给出 plugins.dir，示例段补 plugins.list）。
func Template(header, sections string) ([]byte, error) {
	scalars, err := defaultScalarsWithOverrides()
	if err != nil {
		return nil, err
	}
	document, err := composeDocument(scalars, sections)
	if err != nil {
		return nil, err
	}
	if header != "" {
		document.Content[0].HeadComment = strings.TrimSpace(header)
	}
	return encodeDocument(document)
}

// defaultScalarsWithOverrides 是骨架里实际写出的顶层标量：core 的默认值，套上
// templateOverrides 里那几处有意偏离的示例取值。
func defaultScalarsWithOverrides() ([]Scalar, error) {
	scalars, err := DefaultScalars()
	if err != nil {
		return nil, err
	}
	for i, scalar := range scalars {
		if value, ok := templateOverrides[scalar.Path]; ok {
			scalars[i].Written = value
			scalars[i].Resolved = value
		}
	}
	return scalars, nil
}

// templateOverrides 是骨架里有别于 core 默认值的示例取值。
//
// 只有一处：日志格式默认是 json，骨架给 text。刚生成的配置多半先在本机终端上跑，
// 文本格式比一屏 JSON 好读；要看结构化日志把这一项改回 json 即可。
var templateOverrides = map[string]any{
	"log.format": "text",
}

// ScalarKeys 返回顶层标量的键表条目，按 config.Keys() 的顺序。
func ScalarKeys() []config.Key {
	var keys []config.Key
	for _, key := range config.Keys() {
		if isScalarPath(key.Path) {
			keys = append(keys, key)
		}
	}
	return keys
}

// ScalarPaths 返回顶层标量的路径，按键表顺序。
func ScalarPaths() []string {
	keys := ScalarKeys()
	paths := make([]string, 0, len(keys))
	for _, key := range keys {
		paths = append(paths, key.Path)
	}
	return paths
}

// OtherScalarPaths 返回不在 SettablePaths 里的顶层标量路径，按键表顺序。
//
// config set 的帮助文案用它，把「白名单」与「其余顶层标量」两份清单凑成完整的顶层标量
// 清单：两份都由这里现算，帮助文案里不留手抄的路径名，core 加减一个顶层标量时跟着变。
// 两份都能直接改，白名单只管「路径默认取不到值时也放行」这一件事（见 checkSettable）。
func OtherScalarPaths() []string {
	allowed := make(map[string]bool, len(SettablePaths))
	for _, path := range SettablePaths {
		allowed[path] = true
	}
	var paths []string
	for _, path := range ScalarPaths() {
		if !allowed[path] {
			paths = append(paths, path)
		}
	}
	return paths
}

// KeyOf 返回路径在 core 键表里的那一条。不在表里时返回假。
func KeyOf(path string) (config.Key, bool) {
	for _, key := range config.Keys() {
		if key.Path == path {
			return key, true
		}
	}
	return config.Key{}, false
}

// EncodeValue 把命令行给的字符串按键表里的类型编码成配置里该有的取值。
//
// 写回的是 YAML 标量：整数与布尔不按类型编码的话会落成字符串，重新加载时报
// 「cannot unmarshal !!str into int」，看着像取值写错了，其实是编码错了。其余类型
// （字符串、时长）原样交给 core 解析：Duration 自带 UnmarshalYAML，认 120s 这类写法。
func EncodeValue(path, raw string) (any, error) {
	key, ok := KeyOf(path)
	if !ok {
		return raw, nil
	}
	switch key.Type {
	case "int":
		value, err := strconv.Atoi(raw)
		if err != nil {
			return nil, fmt.Errorf("%s 是整数，%q 不是: %w", path, raw, err)
		}
		return value, nil
	case "bool":
		value, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, fmt.Errorf("%s 是布尔值，%q 不是: %w", path, raw, err)
		}
		return value, nil
	default:
		return raw, nil
	}
}

// isScalarPath 报告路径是不是顶层标量：不含 []，也就是不落在列表元素上。
func isScalarPath(path string) bool {
	return !strings.Contains(path, "[]")
}

// blankValue 把等于零值的取值折成 nil。
//
// 空串、零与 nil 都只在表示「没写」时出现，写出来是它们反而没有信息量（affinity.ttl
// 不写时 core 用 10m，置成 nil 才不会让人以为文件里写了 0s）。
func blankValue(value, zero any) any {
	if reflect.DeepEqual(value, zero) {
		return nil
	}
	return value
}

// writtenValues 返回配置文件里写下的顶层标量取值，键是配置路径。文件不存在时是空表。
//
// 它读的是文件原样，不填默认值也不校验：这里要回答的是「这一项写了没有」，而填过默认值
// 的那份配置答不了 —— 文件里没写的项在它里面也是同一个值。
func (e *Env) writtenValues() (map[string]any, error) {
	values := map[string]any{}
	if !e.HasConfig {
		return values, nil
	}
	//nolint:gosec // 路径来自调用方：配置文件位置由使用者给出。
	source, err := os.ReadFile(e.ConfigPath)
	if err != nil {
		return nil, fmt.Errorf("读取配置文件 %s 失败: %w", e.ConfigPath, err)
	}
	var generic map[string]any
	if err := yaml.Unmarshal(source, &generic); err != nil {
		return nil, fmt.Errorf("解析配置文件 %s 失败: %w", e.ConfigPath, err)
	}
	for _, path := range ScalarPaths() {
		if value, ok := lookupOptional(generic, path); ok {
			values[path] = value
		}
	}
	return values, nil
}

// zeroValues 返回一份零值配置的顶层标量取值，键是配置路径。
//
// core 的 Config 每个字段都进 YAML，因此零值配置的通用值里每条路径都取得到，只是取值
// 都为空或零。拿它与加载后的取值比，就知道某一项是不是写出来的。
func zeroValues() (map[string]any, error) {
	generic, err := (&Env{Config: &config.Config{}}).Generic()
	if err != nil {
		return nil, err
	}
	paths := ScalarPaths()
	values := make(map[string]any, len(paths))
	for _, path := range paths {
		value, err := lookup(generic, path)
		if err != nil {
			return nil, err
		}
		values[path] = value
	}
	return values, nil
}

// lookup 在通用值里按点分路径取一个字段。顶层标量的路径只走映射，不走下标。
func lookup(generic map[string]any, path string) (any, error) {
	value, ok := lookupOptional(generic, path)
	if !ok {
		return nil, fmt.Errorf("配置里没有 %s", path)
	}
	return value, nil
}

// lookupOptional 与 lookup 同理，路径上缺哪一段都返回假。
func lookupOptional(generic map[string]any, path string) (any, bool) {
	current := any(generic)
	for _, segment := range strings.Split(path, ".") {
		node, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		next, ok := node[segment]
		if !ok {
			return nil, false
		}
		current = next
	}
	return current, true
}

// composeDocument 按键表顺序把顶层标量编码成文档节点，再把示例段并进去。
func composeDocument(scalars []Scalar, sections string) (*yaml.Node, error) {
	root := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for _, scalar := range scalars {
		if scalar.Written == nil {
			continue
		}
		node := root
		segments := strings.Split(scalar.Path, ".")
		for _, segment := range segments[:len(segments)-1] {
			node = childMapping(node, segment)
		}
		value := &yaml.Node{}
		if err := value.Encode(scalar.Written); err != nil {
			return nil, fmt.Errorf("编码 %s 失败: %w", scalar.Path, err)
		}
		node.Content = append(node.Content, stringNode(segments[len(segments)-1]), value)
	}
	if err := mergeSections(root, sections); err != nil {
		return nil, err
	}
	return &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{root}}, nil
}

// mergeSections 把示例段的顶层键并进文档根：已有的映射往下并，缺的按示例段的顺序补在
// 末尾。同名但不是映射的（例如示例段又写了一遍 listen）按重复报错，不静默覆盖。
func mergeSections(root *yaml.Node, sections string) error {
	if strings.TrimSpace(sections) == "" {
		return nil
	}
	var fragment yaml.Node
	if err := yaml.Unmarshal([]byte(sections), &fragment); err != nil {
		return fmt.Errorf("解析示例段失败: %w", err)
	}
	if len(fragment.Content) == 0 {
		return nil
	}
	source := fragment.Content[0]
	if source.Kind != yaml.MappingNode {
		return fmt.Errorf("示例段的根节点不是映射")
	}
	for i := 0; i+1 < len(source.Content); i += 2 {
		key, value := source.Content[i], source.Content[i+1]
		existing := childNode(root, key.Value)
		if existing == nil {
			root.Content = append(root.Content, key, value)
			continue
		}
		if existing.Kind != yaml.MappingNode || value.Kind != yaml.MappingNode {
			return fmt.Errorf("示例段里的 %s 与生成的键重复", key.Value)
		}
		existing.Content = append(existing.Content, value.Content...)
	}
	return nil
}

// childMapping 返回映射里 key 对应的子映射，不存在时按顺序补一个。
func childMapping(parent *yaml.Node, key string) *yaml.Node {
	if existing := childNode(parent, key); existing != nil {
		return existing
	}
	node := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	parent.Content = append(parent.Content, stringNode(key), node)
	return node
}

// childNode 返回映射里 key 对应的值节点，不存在时返回 nil。
func childNode(parent *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(parent.Content); i += 2 {
		if parent.Content[i].Value == key {
			return parent.Content[i+1]
		}
	}
	return nil
}

// encodeDocument 用两格缩进把文档编码成字节。
func encodeDocument(document *yaml.Node) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := yaml.NewEncoder(&buffer)
	encoder.SetIndent(2)
	if err := encoder.Encode(document); err != nil {
		return nil, fmt.Errorf("编码配置失败: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return nil, fmt.Errorf("编码配置失败: %w", err)
	}
	return buffer.Bytes(), nil
}

// stringNode 构造一个字符串标量节点。
func stringNode(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
}

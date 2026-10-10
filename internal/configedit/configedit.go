// Package configedit 定点改写配置文件，保留已有注释。
//
// core 的 config 包只负责读。管理命令要把启用状态与顺序写回同一份文件，因此这里用
// yaml.Node 做定点改写：原有节点的注释、引号与顺序都保留，只有被改写的标量重新编码。
// 缩进统一成两格，空行可能被规范化。
package configedit

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"

	"gopkg.in/yaml.v3"
)

// SetPluginField 保证 plugins.list 里存在 key 对应的条目，并把 field 设为 value。
//
// field 只用于标量字段，目前是 enabled 与 order。
func SetPluginField(path, key, field string, value any) error {
	source, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("读取配置文件 %s 失败: %w", path, err)
	}

	var document yaml.Node
	if err := yaml.Unmarshal(source, &document); err != nil {
		return fmt.Errorf("解析配置文件 %s 失败: %w", path, err)
	}

	root, err := rootMapping(&document)
	if err != nil {
		return fmt.Errorf("配置文件 %s: %w", path, err)
	}
	plugins, err := childMapping(root, "plugins")
	if err != nil {
		return fmt.Errorf("配置文件 %s: %w", path, err)
	}
	list, err := childSequence(plugins, "list")
	if err != nil {
		return fmt.Errorf("配置文件 %s: %w", path, err)
	}

	item := findKey(list, key)
	if item == nil {
		item = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		item.Content = append(item.Content, scalarNode("key"), scalarNode(key))
		list.Content = append(list.Content, item)
	}
	if err := setField(item, field, value); err != nil {
		return fmt.Errorf("配置文件 %s: %w", path, err)
	}

	return write(path, source, &document)
}

// Restore 把内容原子写回文件，权限沿用原文件。
func Restore(path string, data []byte) error {
	mode := fs.FileMode(0o600)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	temp := path + ".tmp"
	if err := os.WriteFile(temp, data, mode); err != nil {
		return fmt.Errorf("写入 %s 失败: %w", temp, err)
	}
	if err := os.Rename(temp, path); err != nil {
		_ = os.Remove(temp)
		return fmt.Errorf("替换 %s 失败: %w", path, err)
	}
	return nil
}

// write 用两格缩进重新编码并原子写回。内容没变时不落盘。
func write(path string, source []byte, document *yaml.Node) error {
	var buffer bytes.Buffer
	encoder := yaml.NewEncoder(&buffer)
	encoder.SetIndent(2)
	if err := encoder.Encode(document); err != nil {
		return fmt.Errorf("编码配置失败: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return fmt.Errorf("编码配置失败: %w", err)
	}
	if bytes.Equal(buffer.Bytes(), source) {
		return nil
	}
	return Restore(path, buffer.Bytes())
}

// rootMapping 返回文档的根映射，空文档补一个空映射。
func rootMapping(document *yaml.Node) (*yaml.Node, error) {
	if document.Kind == 0 || len(document.Content) == 0 {
		node := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		document.Kind = yaml.DocumentNode
		document.Content = []*yaml.Node{node}
		return node, nil
	}
	root := document.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("根节点不是映射")
	}
	return root, nil
}

// childMapping 返回映射里 key 对应的子映射，不存在时补一个空映射。
func childMapping(parent *yaml.Node, key string) (*yaml.Node, error) {
	if value := child(parent, key); value != nil {
		if value.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("%s 不是映射", key)
		}
		return value, nil
	}
	node := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	parent.Content = append(parent.Content, scalarNode(key), node)
	return node, nil
}

// childSequence 与 childMapping 同理，取的是序列。
func childSequence(parent *yaml.Node, key string) (*yaml.Node, error) {
	if value := child(parent, key); value != nil {
		if value.Kind != yaml.SequenceNode {
			return nil, fmt.Errorf("%s 不是序列", key)
		}
		return value, nil
	}
	node := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	parent.Content = append(parent.Content, scalarNode(key), node)
	return node, nil
}

// child 返回映射里 key 对应的值节点，不存在时返回 nil。
func child(parent *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(parent.Content); i += 2 {
		if parent.Content[i].Value == key {
			return parent.Content[i+1]
		}
	}
	return nil
}

// findKey 在序列里找 key 字段等于 key 的映射项。
func findKey(list *yaml.Node, key string) *yaml.Node {
	for _, item := range list.Content {
		if item.Kind != yaml.MappingNode {
			continue
		}
		if value := child(item, "key"); value != nil && value.Value == key {
			return item
		}
	}
	return nil
}

// setField 设置映射里的标量字段，值上的注释跟着原值一起保留。
func setField(node *yaml.Node, field string, value any) error {
	replacement, err := scalarValue(value)
	if err != nil {
		return err
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value != field {
			continue
		}
		previous := node.Content[i+1]
		replacement.HeadComment = previous.HeadComment
		replacement.LineComment = previous.LineComment
		replacement.FootComment = previous.FootComment
		node.Content[i+1] = replacement
		return nil
	}
	node.Content = append(node.Content, scalarNode(field), replacement)
	return nil
}

// scalarValue 把 Go 值编码成一个标量节点。
func scalarValue(value any) (*yaml.Node, error) {
	node := &yaml.Node{}
	if err := node.Encode(value); err != nil {
		return nil, fmt.Errorf("编码 %v 失败: %w", value, err)
	}
	return node, nil
}

// scalarNode 构造一个字符串标量节点。
func scalarNode(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
}

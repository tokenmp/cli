package configedit

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// AppendMapping 往点分路径 keys 指向的序列末尾追加一个映射条目。
//
// 最后一段不存在时补出一个空序列：调用方是 channels create 这类「先建段再追加」的
// 命令，配置里还没写 channels 段时应当直接追加成功，而不是让人先去补一个键。
// 中间段不存在或类型不对时报错，不猜结构。
// value 是条目内容，编码后必须是一个映射，字段顺序沿用 value 自身的定义顺序。
//
// 序列原本是空的（channels: []）时，追加后改用块式渲染，与手写的配置一致。
func AppendMapping(path string, keys []string, value any, check func() error) error {
	if len(keys) == 0 {
		return fmt.Errorf("配置文件 %s: 路径为空", path)
	}
	full := strings.Join(keys, ".")
	return Apply(path, func(document *yaml.Node) error {
		root, err := rootMapping(document)
		if err != nil {
			return err
		}
		list, err := appendTarget(root, keys, full)
		if err != nil {
			return err
		}
		item, err := mappingValue(value)
		if err != nil {
			return fmt.Errorf("路径 %s 的条目%w", full, err)
		}
		if len(list.Content) == 0 {
			// channels: [] 解析出来是流式序列，清掉样式让新条目按块式渲染。
			list.Style = 0
		}
		list.Content = append(list.Content, item)
		return nil
	}, check)
}

// appendTarget 返回追加的目标序列，最后一段不存在时补出一个空的。
func appendTarget(root *yaml.Node, keys []string, full string) (*yaml.Node, error) {
	parent, err := parentMapping(root, keys)
	if err != nil {
		return nil, err
	}
	key := keys[len(keys)-1]
	if list := child(parent, key); list != nil {
		if list.Kind != yaml.SequenceNode {
			return nil, fmt.Errorf("路径 %s 不是序列", full)
		}
		return list, nil
	}
	list := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	parent.Content = append(parent.Content, scalarNode(key), list)
	return list, nil
}

// parentMapping 走完 keys 的中间段，返回最后一段所在的映射。
func parentMapping(root *yaml.Node, keys []string) (*yaml.Node, error) {
	current := root
	for i, key := range keys[:len(keys)-1] {
		value := child(current, key)
		if value == nil {
			return nil, fmt.Errorf("路径 %s 不存在", strings.Join(keys[:i+1], "."))
		}
		if value.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("路径 %s 不是映射", strings.Join(keys[:i+1], "."))
		}
		current = value
	}
	return current, nil
}

// RemoveIndex 删除点分路径 keys 指向的序列里下标为 index 的条目。
//
// 下标口径与 config get 一致：从 0 开始，负数与越界都报错。keys 指向的必须是一个
// 已经存在的序列。删到序列为空时写成 channels: []，不留下一个悬空的键。
func RemoveIndex(path string, keys []string, index int, check func() error) error {
	if len(keys) == 0 {
		return fmt.Errorf("配置文件 %s: 路径为空", path)
	}
	full := strings.Join(keys, ".")
	return Apply(path, func(document *yaml.Node) error {
		root, err := rootMapping(document)
		if err != nil {
			return err
		}
		list, err := sequenceAt(root, keys, full)
		if err != nil {
			return err
		}
		if index < 0 || index >= len(list.Content) {
			return fmt.Errorf("路径 %s 的下标 %d 越界，共 %d 条", full, index, len(list.Content))
		}
		list.Content = append(list.Content[:index], list.Content[index+1:]...)
		return nil
	}, check)
}

// sequenceAt 返回根映射下 keys 指向的序列，路径不存在或类型不对时返回中文错误。
//
// full 是 keys 的点分写法，只用于错误信息，让调用方一眼看出是哪条路径出的问题。
func sequenceAt(root *yaml.Node, keys []string, full string) (*yaml.Node, error) {
	current := root
	for i, key := range keys {
		value := child(current, key)
		prefix := strings.Join(keys[:i+1], ".")
		if value == nil {
			return nil, fmt.Errorf("路径 %s 不存在", prefix)
		}
		if i == len(keys)-1 {
			if value.Kind != yaml.SequenceNode {
				return nil, fmt.Errorf("路径 %s 不是序列", full)
			}
			return value, nil
		}
		if value.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("路径 %s 不是映射", prefix)
		}
		current = value
	}
	return nil, fmt.Errorf("路径 %s 为空", full)
}

// mappingValue 把 Go 值编码成一个映射节点，值不是映射时报错。
func mappingValue(value any) (*yaml.Node, error) {
	node := &yaml.Node{}
	if err := node.Encode(value); err != nil {
		return nil, fmt.Errorf("编码失败: %w", err)
	}
	// Node.Encode 直接设成值对应的节点，不包文档节点；两种形状都处理。
	if node.Kind == yaml.DocumentNode {
		if len(node.Content) == 0 {
			return nil, fmt.Errorf("不是映射")
		}
		node = node.Content[0]
	}
	if node.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("不是映射")
	}
	return node, nil
}

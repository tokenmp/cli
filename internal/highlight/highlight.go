// Package highlight 给源码文本上色。
//
// 高亮引擎是 chroma，bat 用的也是它，配色因此与 bat 接近。样式按终端明暗选：深色
// 背景用 monokai，浅色背景用 monokailight。终端能力不足时由调用方的写入器降级颜色，
// 这里统一输出 24 位色。
package highlight

import (
	"io"
	"os"

	"charm.land/lipgloss/v2"
	"github.com/alecthomas/chroma/v2/quick"
)

// 主题名。
const (
	DarkStyle  = "monokai"
	LightStyle = "monokailight"
)

// YAML 把 YAML 源码上色写到 w。
//
// 每行自带颜色复位，并在下一行重新开始，因此调用方可以按行插入行号栏。
func YAML(w io.Writer, source string) error {
	return quick.Highlight(w, source, "yaml", "terminal16m", Style())
}

// Style 返回当前终端该用的主题名。
func Style() string {
	if lipgloss.HasDarkBackground(os.Stdin, os.Stdout) {
		return DarkStyle
	}
	return LightStyle
}

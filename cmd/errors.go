package cmd

import (
	"fmt"
	"io"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/fang"
	"github.com/charmbracelet/x/term"
)

// usageErrorPrefixes 与 fang 判断用法错误的依据一致：这类错误由 cobra 的参数与标志
// 解析产生，值得再给一句 --help 的提示。
var usageErrorPrefixes = []string{
	"flag needs an argument:",
	"unknown flag:",
	"unknown shorthand flag:",
	"unknown command",
	"invalid argument",
}

// errorHandler 打印命令返回的错误。
//
// fang 的默认处理器按英文习惯把首词首字母大写并补一个句点。本工具的错误文案是中文，
// 这两步只会添乱，其余排版与默认处理器保持一致。
func errorHandler(w io.Writer, styles fang.Styles, err error) {
	if file, ok := w.(term.File); ok && !term.IsTerminal(file.Fd()) {
		fmt.Fprintln(w, err.Error())
		return
	}

	fmt.Fprintln(w, styles.ErrorHeader.String())
	fmt.Fprintln(w, styles.ErrorText.UnsetTransform().Render(err.Error()))
	fmt.Fprintln(w)

	if isUsageError(err) {
		fmt.Fprintln(w, lipgloss.JoinHorizontal(
			lipgloss.Left,
			styles.ErrorText.UnsetWidth().UnsetMargins().UnsetTransform().Render("用法见"),
			styles.Program.Flag.Render(" --help "),
		))
		fmt.Fprintln(w)
	}
}

// isUsageError 报告错误是否来自参数或标志解析。
func isUsageError(err error) bool {
	for _, prefix := range usageErrorPrefixes {
		if strings.HasPrefix(err.Error(), prefix) {
			return true
		}
	}
	return false
}

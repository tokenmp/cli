package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/exp/charmtone"
	"github.com/spf13/cobra"

	"github.com/tokenmp/core/plugin"
)

// palette 是各命令共用的配色，取值与 fang 的默认配色同源，帮助页面与命令输出看起来
// 是同一个工具。颜色代码始终写出，终端不支持颜色时由 colorprofile 的写入器去掉。
var palette = newPalette()

func newPalette() theme {
	// 终端明暗决定用哪一组颜色：Charcoal 在深色背景上看不清，Ash 在浅色背景上
	// 看不清，其余同理。
	pick := lipgloss.LightDark(lipgloss.HasDarkBackground(os.Stdin, os.Stdout))
	return theme{
		program: lipgloss.NewStyle().Bold(true).Foreground(pick(charmtone.Malibu, charmtone.Guppy)),
		title:   lipgloss.NewStyle().Bold(true).Foreground(charmtone.Charple),
		cell:    lipgloss.NewStyle().Foreground(pick(charmtone.Charcoal, charmtone.Ash)),
		label:   lipgloss.NewStyle().Foreground(pick(charmtone.Oyster, charmtone.Squid)),
		border:  lipgloss.NewStyle().Foreground(pick(charmtone.Smoke, charmtone.Oyster)),
		ok:      lipgloss.NewStyle().Foreground(charmtone.Guac),
		warn:    lipgloss.NewStyle().Foreground(pick(charmtone.Cumin, charmtone.Mustard)),
		fail:    lipgloss.NewStyle().Foreground(pick(charmtone.Sriracha, charmtone.Coral)),
	}
}

// theme 是一组已渲染好的样式。
type theme struct {
	program lipgloss.Style
	title   lipgloss.Style
	cell    lipgloss.Style
	label   lipgloss.Style
	border  lipgloss.Style
	ok      lipgloss.Style
	warn    lipgloss.Style
	fail    lipgloss.Style
}

// out 返回命令的输出目标：终端能力不够时颜色会被去掉。
func out(cmd *cobra.Command) io.Writer {
	return colorprofile.NewWriter(cmd.OutOrStdout(), os.Environ())
}

// newTable 返回一张只有表头分隔线的表格，配色与帮助页面一致。
func newTable() *table.Table {
	return table.New().
		Border(lipgloss.NormalBorder()).
		BorderStyle(palette.border).
		BorderTop(false).
		BorderBottom(false).
		BorderLeft(false).
		BorderRight(false).
		BorderColumn(false).
		BorderRow(false).
		BorderHeader(true).
		StyleFunc(func(row, _ int) lipgloss.Style {
			// 列之间没有竖线，靠单元格内边距把列分开。
			if row == table.HeaderRow {
				return palette.title.Padding(0, 1)
			}
			return palette.cell.Padding(0, 1)
		})
}

// printFields 打印标签与值对齐的字段表。字段比表格窄的地方多，这种排法更省地方。
//
// 宽度按最长标签加冒号计算，标签本身只用 ASCII，因此可以直接用字节数比较。
func printFields(w io.Writer, rows [][2]string) {
	width := 0
	for _, row := range rows {
		if current := lipgloss.Width(row[0]) + 1; current > width {
			width = current
		}
	}
	for _, row := range rows {
		label := palette.label.Width(width).Render(row[0] + ":")
		fmt.Fprintf(w, "%s %s\n", label, palette.cell.Render(row[1]))
	}
}

// printJSON 以两格缩进的 JSON 写出值，供脚本消费。
func printJSON(cmd *cobra.Command, value any) error {
	encoder := json.NewEncoder(cmd.OutOrStdout())
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return fmt.Errorf("写出 JSON 失败: %w", err)
	}
	return nil
}

// joinList 用顿号连接一组词。
func joinList(items []string) string {
	return strings.Join(items, "、")
}

// joinHooks 把钩子名连成一行，没有钩子时显示占位符。
func joinHooks(hooks []plugin.Hook) string {
	if len(hooks) == 0 {
		return "-"
	}
	names := make([]string, 0, len(hooks))
	for _, hook := range hooks {
		names = append(names, string(hook))
	}
	return strings.Join(names, ", ")
}

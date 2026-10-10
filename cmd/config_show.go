package cmd

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/tokenmp/cli/internal/configmgr"
	"github.com/tokenmp/cli/internal/highlight"
)

// show 命令支持的输出格式。
const (
	formatUI   = "ui"
	formatYAML = "yaml"
	formatJSON = "json"
)

// showFormats 是 --format 的取值，顺序即文档里的顺序。
var showFormats = []string{formatUI, formatYAML, formatJSON}

// headerWidth 是拿不到终端宽度时表头用的列数。
const headerWidth = 80

func newConfigShowCmd(opts *configOptions) *cobra.Command {
	var format string

	cmd := &cobra.Command{
		Use:   "show",
		Short: "打印生效配置",
		Long: `打印生效配置。

默认输出高亮的配置文本：YAML 按语法上色，左边一列行号，顶上一条表头，样式与 bat
接近。输出不是终端时与 --format yaml 完全一致，行号与表头不会混进重定向的文件。

--format yaml 给不带装饰的配置文本，适合重定向成一份新配置；--format json 给机器
可读的形状。--json 等同于 --format json。

三种格式看到的都是生效配置，不是文件原文，因此没写在文件里的 listen、timeout、
max_retries 也会出现。yaml 与 json 不打印空值字段：空字符串、空段落与空列表只表示
没写，没有信息量。`,
		Example: `  tokenmp config show
  tokenmp config show --format yaml > /tmp/effective.yaml
  tokenmp config show --format json
  tokenmp config show --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			env, err := configmgr.Load(opts.configPath)
			if err != nil {
				return err
			}

			// --json 是整组通用的开关，它压过 --format。
			selected := format
			if opts.json {
				selected = formatJSON
			}

			switch selected {
			case formatUI:
				return renderConfigSource(cmd, env)
			case formatYAML:
				return printConfigYAML(cmd, env)
			case formatJSON:
				generic, err := env.Pruned()
				if err != nil {
					return err
				}
				return printJSON(cmd, generic)
			default:
				return fmt.Errorf("--format 只支持 %s，实际是 %q", strings.Join(showFormats, "、"), selected)
			}
		},
	}

	// 说明里要留一个空格：fang 只对第一个空格分隔的词做首字母大写，整串没有空格时
	// 它会把 ui、yaml、json 一起改写成 Ui、Yaml、Json。
	cmd.Flags().StringVar(&format, "format", formatUI, "输出格式，可选 "+strings.Join(showFormats, "、"))
	return cmd
}

// printConfigYAML 打印生效配置的 YAML 文本，不带任何装饰。
func printConfigYAML(cmd *cobra.Command, env *configmgr.Env) error {
	encoded, err := env.PrunedYAML()
	if err != nil {
		return err
	}
	writer := out(cmd)
	fmt.Fprintf(writer, "# %s\n", env.ConfigPath)
	fmt.Fprint(writer, string(encoded))
	return nil
}

// renderConfigSource 按 bat 的样子打印配置：表头、行号栏、按语法上色的正文。
//
// 输出不是终端时退化成 --format yaml 的纯文本。
func renderConfigSource(cmd *cobra.Command, env *configmgr.Env) error {
	source, err := env.PrunedYAML()
	if err != nil {
		return err
	}
	writer := out(cmd)
	if !isTerminal(cmd) {
		// 不是终端时与 --format yaml 输出完全一致：行号与表头是给眼睛看的，重定向时
		// 只会碍事。
		fmt.Fprintf(writer, "# %s\n", env.ConfigPath)
		fmt.Fprint(writer, string(source))
		return nil
	}

	var highlighted bytes.Buffer
	if err := highlight.YAML(&highlighted, string(source)); err != nil {
		return fmt.Errorf("高亮配置失败: %w", err)
	}
	lines := strings.Split(strings.TrimSuffix(highlighted.String(), "\n"), "\n")

	fmt.Fprintln(writer, headerLine(env.ConfigPath))
	numbers := palette.label.Width(len(strconv.Itoa(len(lines))))
	for index, line := range lines {
		fmt.Fprintf(writer, "%s %s %s\n", numbers.Render(strconv.Itoa(index+1)), palette.label.Render("│"), line)
	}
	return nil
}

// headerLine 渲染表头：一条横线夹着配置文件路径。
func headerLine(path string) string {
	label := " " + path + " "
	rule := terminalWidth() - lipgloss.Width(label) - len("───")
	if rule < 3 {
		rule = 3
	}
	return palette.label.Render("───" + label + strings.Repeat("─", rule))
}

// terminalWidth 返回终端列数，拿不到时给一个默认值。
func terminalWidth() int {
	if width, _, err := term.GetSize(os.Stdout.Fd()); err == nil && width > 0 {
		return width
	}
	return headerWidth
}

// isTerminal 报告命令的输出是不是终端。
func isTerminal(cmd *cobra.Command) bool {
	file, ok := cmd.OutOrStdout().(term.File)
	return ok && term.IsTerminal(file.Fd())
}

package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

// completionShells 是可用的 shell，也是报错时列出的取值，顺序固定。
var completionShells = []string{"bash", "zsh", "fish", "powershell"}

// newCompletionInstallCmd 造 completion install 子命令。
func newCompletionInstallCmd() *cobra.Command {
	var (
		dir    string
		dryRun bool
	)

	cmd := &cobra.Command{
		Use:   "install [shell]",
		Short: "把补全脚本装进 shell 的查找目录",
		Long: `把补全脚本装进 shell 的查找目录。

不指定 shell 时按 $SHELL 猜，取基名后匹配。落点按 XDG 约定：

- bash：$XDG_DATA_HOME/bash-completion/completions/tokenmp
- zsh：$XDG_DATA_HOME/zsh/site-functions/_tokenmp
- fish：$XDG_CONFIG_HOME/fish/completions/tokenmp.fish
- powershell：没有补全查找目录，只在 $PROFILE 里生效，这里只打印提示不写文件

$XDG_DATA_HOME 默认 ~/.local/share，$XDG_CONFIG_HOME 默认 ~/.config。
目录不存在时创建，文件已存在时覆盖，重复执行结果相同。
zsh 装完还要把该目录加进 fpath 才会生效。
--dir 换成指定的目录，--dry-run 只看将要写入的路径。`,
		Example: `  # 按 $SHELL 装到默认落点
  tokenmp completion install

  # 明确指定 shell
  tokenmp completion install zsh

  # 装到自定义目录，只看将要写入的路径
  tokenmp completion install bash --dir ~/.config/bash-completion/completions --dry-run`,
		Args:      cobra.MaximumNArgs(1),
		ValidArgs: completionShells,
		RunE: func(cmd *cobra.Command, args []string) error {
			shell, err := resolveShell(args)
			if err != nil {
				return err
			}

			writer := out(cmd)

			// PowerShell 没有补全查找目录，脚本只能靠 $PROFILE 手动加载。
			if shell == "powershell" {
				fmt.Fprintf(writer, "%s\n", palette.warn.Render("PowerShell 没有补全查找目录，把下面一行加进 $PROFILE"))
				fmt.Fprintf(writer, "  tokenmp completion powershell | Out-String | Invoke-Expression\n")
				return nil
			}

			target := completionTarget(shell, dir)
			if dryRun {
				fmt.Fprintf(writer, "%s %s\n", palette.label.Render("将写入"), target)
				return nil
			}

			script, err := generateCompletion(cmd.Root(), shell)
			if err != nil {
				return err
			}
			parent := filepath.Dir(target)
			if err := os.MkdirAll(parent, 0o755); err != nil {
				return fmt.Errorf("创建目录 %s 失败: %w", parent, err)
			}
			if err := os.WriteFile(target, script, 0o644); err != nil {
				return fmt.Errorf("写入 %s 失败: %w", target, err)
			}
			fmt.Fprintf(writer, "%s %s\n", palette.ok.Render("已写入"), target)

			if shell == "zsh" {
				fmt.Fprintf(writer, "\n把这一行加进 ~/.zshrc，补全才会生效：\n  %s\n", zshFpathLine(dir))
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&dir, "dir", "", "安装目录，默认按 XDG 约定")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "只打印将要写入的路径，不落盘")
	return cmd
}

// resolveShell 取 install 的 shell 参数；没给时按 $SHELL 猜，猜不出就报错。
func resolveShell(args []string) (string, error) {
	if len(args) == 1 {
		if !isCompletionShell(args[0]) {
			return "", fmt.Errorf("不支持的 shell %q，可选值：%s", args[0], joinList(completionShells))
		}
		return args[0], nil
	}

	base := filepath.Base(os.Getenv("SHELL"))
	// Windows 上的 PowerShell 可执行文件名是 pwsh，归一成药补全用的名字。
	if base == "pwsh" {
		return "powershell", nil
	}
	if isCompletionShell(base) {
		return base, nil
	}
	return "", fmt.Errorf("从 $SHELL 猜不出 shell，请显式给出：%s", joinList(completionShells))
}

// isCompletionShell 报告名字是否是可选 shell 之一。
func isCompletionShell(shell string) bool {
	for _, name := range completionShells {
		if name == shell {
			return true
		}
	}
	return false
}

// completionTarget 给出补全脚本的路径。dir 非空时用它，否则按 XDG 约定。
func completionTarget(shell, dir string) string {
	name := completionFileName(shell)
	if dir != "" {
		return filepath.Join(dir, name)
	}
	switch shell {
	case "bash":
		return filepath.Join(xdgDataHome(), "bash-completion", "completions", name)
	case "zsh":
		return filepath.Join(xdgDataHome(), "zsh", "site-functions", name)
	default:
		return filepath.Join(xdgConfigHome(), "fish", "completions", name)
	}
}

// completionFileName 是各 shell 补全脚本的文件名。bash 认文件有没有 .bash 后缀，
// 这里用不带后缀的形式。
func completionFileName(shell string) string {
	switch shell {
	case "zsh":
		return "_tokenmp"
	case "fish":
		return "tokenmp.fish"
	default:
		return "tokenmp"
	}
}

// zshFpathLine 给出 zsh 的 fpath 设置行。指定 --dir 时直接写该目录，否则用 XDG
// 变量加默认值的写法，这一行原样复制进配置即可。
func zshFpathLine(dir string) string {
	if dir != "" {
		return "fpath+=(" + dir + ")"
	}
	return "fpath+=${XDG_DATA_HOME:-$HOME/.local/share}/zsh/site-functions"
}

// generateCompletion 按 shell 生成补全脚本，内容与 completion <shell> 的输出一致。
func generateCompletion(root *cobra.Command, shell string) ([]byte, error) {
	var buf bytes.Buffer

	var err error
	switch shell {
	case "bash":
		err = root.GenBashCompletionV2(&buf, true)
	case "zsh":
		err = root.GenZshCompletion(&buf)
	case "fish":
		err = root.GenFishCompletion(&buf, true)
	default:
		return nil, fmt.Errorf("不支持的 shell %q，可选值：%s", shell, joinList(completionShells))
	}
	if err != nil {
		return nil, fmt.Errorf("生成 %s 补全脚本失败: %w", shell, err)
	}
	return buf.Bytes(), nil
}

// xdgDataHome 返回 $XDG_DATA_HOME，未设置时按 XDG 规范回退到 ~/.local/share。
func xdgDataHome() string {
	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
		return dir
	}
	return filepath.Join(homeDir(), ".local", "share")
}

// xdgConfigHome 返回 $XDG_CONFIG_HOME，未设置时按 XDG 规范回退到 ~/.config。
func xdgConfigHome() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return dir
	}
	return filepath.Join(homeDir(), ".config")
}

// homeDir 取用户主目录；取不到时退回当前目录，避免拼出一个空的绝对路径。
func homeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return home
}

// localizeCompletion 把 cobra 自动生成的 completion 命令换成中文说明，并让补全脚本
// 写到执行时的输出目标。
//
// cobra 在创建 completion 子命令时就把当时的输出目标固化进了 RunE 的闭包，
// 而 localize 在 SetOut 之前跑，于是那个目标停在 os.Stdout。这里换成运行时取
// cmd.OutOrStdout() 的实现，生成参数与 cobra 自带的一致。
func localizeCompletion(cmd *cobra.Command) {
	cmd.Short = "生成 shell 补全脚本"
	cmd.Long = `生成 shell 补全脚本。

脚本按 shell 输出到标准输出，用 install 一步装进 shell 的查找目录：

- bash：tokenmp completion bash
- zsh：tokenmp completion zsh
- fish：tokenmp completion fish
- powershell：tokenmp completion powershell`

	for _, child := range cmd.Commands() {
		switch child.Name() {
		case "bash":
			child.Short = "生成 bash 补全脚本"
			child.RunE = func(c *cobra.Command, _ []string) error {
				return c.Root().GenBashCompletionV2(c.OutOrStdout(), !completionNoDesc(c))
			}
		case "zsh":
			child.Short = "生成 zsh 补全脚本"
			child.RunE = func(c *cobra.Command, _ []string) error {
				if completionNoDesc(c) {
					return c.Root().GenZshCompletionNoDesc(c.OutOrStdout())
				}
				return c.Root().GenZshCompletion(c.OutOrStdout())
			}
		case "fish":
			child.Short = "生成 fish 补全脚本"
			child.RunE = func(c *cobra.Command, _ []string) error {
				return c.Root().GenFishCompletion(c.OutOrStdout(), !completionNoDesc(c))
			}
		case "powershell":
			child.Short = "生成 PowerShell 补全脚本"
			child.RunE = func(c *cobra.Command, _ []string) error {
				if completionNoDesc(c) {
					return c.Root().GenPowerShellCompletion(c.OutOrStdout())
				}
				return c.Root().GenPowerShellCompletionWithDesc(c.OutOrStdout())
			}
		}

		if flag := child.Flags().Lookup("no-descriptions"); flag != nil {
			flag.Usage = "不生成描述"
		}
	}
}

// completionNoDesc 读取 cobra 挂在补全子命令上的 --no-descriptions，默认关。
func completionNoDesc(cmd *cobra.Command) bool {
	if cmd.Flags().Lookup("no-descriptions") == nil {
		return false
	}
	value, err := cmd.Flags().GetBool("no-descriptions")
	return err == nil && value
}

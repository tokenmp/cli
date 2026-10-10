package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// completionSandbox 把 XDG 基础目录指到临时目录，并把 $SHELL 清空，避免测试之间
// 相互影响。返回 data 与 config 两个基础目录。
func completionSandbox(t *testing.T) (dataHome, configHome string) {
	t.Helper()

	root := t.TempDir()
	dataHome = filepath.Join(root, "data")
	configHome = filepath.Join(root, "config")
	t.Setenv("XDG_DATA_HOME", dataHome)
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("SHELL", "")
	return dataHome, configHome
}

func TestCompletionScripts(t *testing.T) {
	for _, shell := range completionShells {
		t.Run(shell, func(t *testing.T) {
			out, err := run(t, "completion", shell)
			if err != nil {
				t.Fatalf("completion %s: %v", shell, err)
			}
			if !strings.Contains(out, "tokenmp") {
				t.Errorf("completion %s 输出里没有 tokenmp:\n%s", shell, out)
			}
		})
	}
}

func TestCompletionInstallWritesScript(t *testing.T) {
	cases := map[string]func(dataHome, configHome string) string{
		"bash": func(dataHome, _ string) string {
			return filepath.Join(dataHome, "bash-completion", "completions", "tokenmp")
		},
		"zsh": func(dataHome, _ string) string {
			return filepath.Join(dataHome, "zsh", "site-functions", "_tokenmp")
		},
		"fish": func(_, configHome string) string {
			return filepath.Join(configHome, "fish", "completions", "tokenmp.fish")
		},
	}

	for shell, target := range cases {
		t.Run(shell, func(t *testing.T) {
			dataHome, configHome := completionSandbox(t)
			path := target(dataHome, configHome)

			out, err := run(t, "completion", "install", shell)
			if err != nil {
				t.Fatalf("completion install %s: %v\n%s", shell, err, out)
			}
			if !strings.Contains(out, path) {
				t.Errorf("输出里没有写入路径 %s:\n%s", path, out)
			}

			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("补全脚本没有写进 %s: %v", path, err)
			}
			want, err := run(t, "completion", shell)
			if err != nil {
				t.Fatalf("completion %s: %v", shell, err)
			}
			if string(got) != want {
				t.Errorf("写入的脚本与 completion %s 的输出不一致\n写入:\n%s\n输出:\n%s", shell, got, want)
			}
		})
	}
}

func TestCompletionInstallRejectsExtraArgs(t *testing.T) {
	completionSandbox(t)

	if _, err := run(t, "completion", "install", "bash", "zsh"); err == nil {
		t.Fatal("多余的 shell 参数应当被拒绝")
	}
}

func TestCompletionInstallOverwritesExistingFile(t *testing.T) {
	dataHome, _ := completionSandbox(t)
	path := filepath.Join(dataHome, "bash-completion", "completions", "tokenmp")
	writeFile(t, path, "旧内容")

	if _, err := run(t, "completion", "install", "bash"); err != nil {
		t.Fatalf("覆盖已有文件时应当成功: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s 失败: %v", path, err)
	}
	want, err := run(t, "completion", "bash")
	if err != nil {
		t.Fatalf("completion bash: %v", err)
	}
	if string(got) != want {
		t.Errorf("覆盖后内容不对:\n%s", got)
	}
}

func TestCompletionInstallDryRunDoesNotWrite(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		t.Run(shell, func(t *testing.T) {
			dataHome, configHome := completionSandbox(t)

			out, err := run(t, "completion", "install", shell, "--dry-run")
			if err != nil {
				t.Fatalf("completion install %s --dry-run: %v", shell, err)
			}
			if !strings.Contains(out, "将写入") {
				t.Errorf("--dry-run 应当说明将写入的路径:\n%s", out)
			}

			// 两个基础目录下都不应有文件落盘。
			for _, base := range []string{dataHome, configHome} {
				if _, err := os.Stat(base); !os.IsNotExist(err) {
					t.Errorf("--dry-run 不应创建目录 %s: %v", base, err)
				}
			}
		})
	}
}

func TestCompletionInstallDirOverride(t *testing.T) {
	completionSandbox(t)
	dir := t.TempDir()

	if _, err := run(t, "completion", "install", "zsh", "--dir", dir); err != nil {
		t.Fatalf("completion install zsh --dir: %v", err)
	}

	path := filepath.Join(dir, "_tokenmp")
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("补全脚本没有写进 %s: %v", path, err)
	}
	want, err := run(t, "completion", "zsh")
	if err != nil {
		t.Fatalf("completion zsh: %v", err)
	}
	if string(got) != want {
		t.Errorf("--dir 写入的内容与 completion zsh 的输出不一致")
	}
}

func TestCompletionInstallZshPrintsFpath(t *testing.T) {
	completionSandbox(t)

	out, err := run(t, "completion", "install", "zsh")
	if err != nil {
		t.Fatalf("completion install zsh: %v", err)
	}
	const want = "fpath+=${XDG_DATA_HOME:-$HOME/.local/share}/zsh/site-functions"
	if !strings.Contains(out, want) {
		t.Errorf("zsh 安装后应当打印 fpath 设置:\n%s", out)
	}
}

func TestCompletionInstallGuessesShellFromEnv(t *testing.T) {
	dataHome, _ := completionSandbox(t)
	t.Setenv("SHELL", "/bin/zsh")

	out, err := run(t, "completion", "install")
	if err != nil {
		t.Fatalf("completion install: %v\n%s", err, out)
	}
	path := filepath.Join(dataHome, "zsh", "site-functions", "_tokenmp")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("没有按 $SHELL 装到 %s: %v", path, err)
	}
	if !strings.Contains(out, "fpath+=") {
		t.Errorf("猜成 zsh 后也应打印 fpath 提示:\n%s", out)
	}
}

func TestCompletionInstallRejectsUnknownShell(t *testing.T) {
	completionSandbox(t)

	// 显式给出一个不支持的 shell。
	if _, err := run(t, "completion", "install", "tcsh"); err == nil {
		t.Fatal("不支持的 shell 应当报错")
	} else if !strings.Contains(err.Error(), "fish") {
		t.Errorf("报错应当列出可选值: %v", err)
	}

	// $SHELL 猜不出来时同样报错。
	t.Setenv("SHELL", "/bin/ksh")
	if _, err := run(t, "completion", "install"); err == nil {
		t.Fatal("$SHELL 猜不出时应当报错")
	}
}

func TestCompletionInstallPowerShellPrintsHint(t *testing.T) {
	completionSandbox(t)

	out, err := run(t, "completion", "install", "powershell")
	if err != nil {
		t.Fatalf("completion install powershell: %v", err)
	}
	if !strings.Contains(out, "$PROFILE") {
		t.Errorf("PowerShell 应当提示写 $PROFILE:\n%s", out)
	}
	if strings.Contains(out, "已写入") {
		t.Errorf("PowerShell 不应写文件:\n%s", out)
	}
}

func TestCompletionHelpIsChinese(t *testing.T) {
	out, err := run(t, "help", "completion")
	if err != nil {
		t.Fatalf("help completion: %v", err)
	}
	if !strings.Contains(out, "生成 shell 补全脚本") {
		t.Errorf("completion 的说明应当是中文:\n%s", out)
	}
}

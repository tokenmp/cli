package cmd

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/tokenmp/core/plugin"

	"github.com/tokenmp/cli/internal/pluginmgr"
)

// errCheckFailed 让检查命令以非零状态退出。问题明细已经打印过，这里只给结论。
var errCheckFailed = errors.New("检查未通过，存在致命问题")

func newPluginCheckCmd(opts *pluginOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "check [key...]",
		Short: "检查插件的语法、自述信息与配置登记",
		Long: `检查插件。

检查三件事：

- 源码能否编译并导出 meta 与可用的钩子
- 源码里是否用到了宿主不提供的能力：网络、require 与动态代码
- 配置 plugins.list 段与插件目录是否一致

给出 key 时只检查这些插件。

有致命问题时以状态码 1 退出，提示不改变退出码。`,
		Example: `  # 目录里的插件全部检查一遍
  tokenmp plugin check

  # 只检查两个
  tokenmp plugin check echo html-escape

  # 在 CI 里看 JSON
  tokenmp plugin check --json`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			env, err := pluginmgr.Load(opts.configPath, opts.dir)
			if err != nil {
				return err
			}
			issues, inspected, err := collectIssues(env, args)
			if err != nil {
				return err
			}

			if opts.json {
				if err := printJSON(cmd, issues); err != nil {
					return err
				}
			} else {
				renderIssues(cmd, env, issues, inspected)
			}

			if countErrors(issues) > 0 {
				return errCheckFailed
			}
			return nil
		},
	}
}

// collectIssues 汇总检查结论，并给出实际检查了几个插件文件。
//
// 目录里读不出的插件、配置与目录的不一致都算结论。插件目录不存在时按 core 的判定
// 处理：没启用任何插件就算没问题，启用了插件则是致命问题。
func collectIssues(env *pluginmgr.Env, keys []string) ([]plugin.Issue, int, error) {
	files, err := env.Files()
	if err != nil {
		return nil, 0, err
	}
	if len(files) == 0 && !env.Exists() {
		enabled := env.Config.EnabledPlugins()
		if len(enabled) == 0 {
			return nil, 0, nil
		}
		names := make([]string, 0, len(enabled))
		for _, entry := range enabled {
			names = append(names, entry.Key)
		}
		return []plugin.Issue{{
			Path:     env.Dir,
			Severity: plugin.SeverityError,
			Message:  fmt.Sprintf("插件目录不存在，但配置里启用了 %s", joinList(names)),
		}}, 0, nil
	}

	limits := plugin.DefaultLimits()
	selected := files
	if len(keys) > 0 {
		selected = nil
		for _, key := range keys {
			file, ok := lookupFile(files, key)
			if !ok {
				selected = append(selected, pluginmgr.File{Key: key, Path: filepath.Join(env.Dir, key)})
				continue
			}
			selected = append(selected, file)
		}
	}

	var issues []plugin.Issue
	for _, file := range selected {
		found, err := plugin.LintFile(file.Path, limits)
		if err != nil {
			return nil, 0, err
		}
		issues = append(issues, found...)
	}

	if len(keys) > 0 || !env.HasConfig {
		// 指定了 key，或没有配置文件时，登记与目录的比对没有意义。
		return issues, len(selected), nil
	}
	return append(issues, registrationIssues(env, files)...), len(selected), nil
}

// registrationIssues 比对配置里的登记项与目录里的文件。
func registrationIssues(env *pluginmgr.Env, files []pluginmgr.File) []plugin.Issue {
	present := make(map[string]bool, len(files))
	for _, file := range files {
		present[file.Key] = true
	}

	registered := make(map[string]bool, len(env.Config.Plugins.List))
	var issues []plugin.Issue
	for _, entry := range env.Config.Plugins.List {
		registered[entry.Key] = true
		if present[entry.Key] {
			continue
		}
		issues = append(issues, plugin.Issue{
			Path:     env.ConfigPath,
			Severity: plugin.SeverityError,
			Message:  fmt.Sprintf("配置登记了插件 %s，但 %s 里没有对应文件", entry.Key, env.Dir),
		})
	}
	for _, file := range files {
		if registered[file.Key] {
			continue
		}
		issues = append(issues, plugin.Issue{
			Path:     env.ConfigPath,
			Severity: plugin.SeverityWarning,
			Message:  fmt.Sprintf("插件 %s 没有登记在 plugins.list 里，转发时不会执行", file.Key),
		})
	}
	return issues
}

// lookupFile 按 key 找候选文件。
func lookupFile(files []pluginmgr.File, key string) (pluginmgr.File, bool) {
	for _, file := range files {
		if file.Key == key {
			return file, true
		}
	}
	return pluginmgr.File{}, false
}

// countErrors 数致命结论。
func countErrors(issues []plugin.Issue) int {
	count := 0
	for _, issue := range issues {
		if issue.IsError() {
			count++
		}
	}
	return count
}

// renderIssues 打印检查结论与一行小结。
func renderIssues(cmd *cobra.Command, env *pluginmgr.Env, issues []plugin.Issue, inspected int) {
	writer := out(cmd)
	if len(issues) == 0 {
		if inspected == 0 {
			fmt.Fprintf(writer, "插件目录 %s 里没有插件，无需检查\n", env.Dir)
			return
		}
		fmt.Fprintf(writer, "%s 的 %d 个插件全部通过检查\n", env.Dir, inspected)
		return
	}

	renderer := newTable().Headers("级别", "对象", "说明")
	for _, issue := range issues {
		renderer.Row(severityLabel(issue), issue.Path, issue.Message)
	}
	fmt.Fprintln(writer, renderer.Render())
	fmt.Fprintf(writer, "\n共 %d 处致命问题，%d 处提示\n", countErrors(issues), len(issues)-countErrors(issues))
}

// severityLabel 把级别渲染成带颜色的短词。
func severityLabel(issue plugin.Issue) string {
	if issue.IsError() {
		return palette.fail.Render("致命")
	}
	return palette.warn.Render("提示")
}

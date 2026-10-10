package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tokenmp/core/plugin"

	"github.com/tokenmp/cli/internal/pluginmgr"
)

// keyPattern 限定插件 key 的字符集。key 会拼进路径，因此必须排除分隔符与点点。
var keyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// pluginTemplate 是新建插件的骨架：一份 meta 与一个不改写的钩子。
const pluginTemplate = `export const meta = {
  key: "%s",
  name: "%s",
  version: "0.1.0",
  apiVersion: "v1",
};

// 钩子按请求生命周期执行，必须是同步函数，返回 Promise 会被拒绝。
// 可用钩子：onRequest、onEvent、onStreamEnd、onResponse。
// 返回 null 或 undefined 表示不改写，宿主沿用前一个插件的输出。
export function onRequest(input) {
  // input: { dialect, model, stream, body }
  // 返回 { model: input.model, body: input.body } 即替换对应字段。
  return null;
}
`

// fixtureTemplate 是新建插件的回放用例骨架。
const fixtureTemplate = `{
  "unixNow": 1735689600,
  "cases": [
    {
      "name": "不改写时返回 null",
      "hook": "onRequest",
      "args": [{ "dialect": "openai", "model": "gpt-4o", "stream": false, "body": {} }],
      "expected": null
    }
  ]
}
`

func newPluginNewCmd(opts *pluginOptions) *cobra.Command {
	var (
		name        string
		withFixture bool
		force       bool
	)

	cmd := &cobra.Command{
		Use:   "new <key>",
		Short: "生成一个插件骨架",
		Long: `生成一个插件骨架。

在插件目录里创建 <key>/plugin.js，默认连一份可回放的 fixture 一起创建。骨架里的
meta.key 与目录名一致，生成后可以直接 check 与 replay。

生成的插件不会自动登记到配置，需要时用 plugin enable 启用。目标文件已存在时
报错，除非给出 --force。`,
		Example: `  # 生成插件与 fixture 骨架
  tokenmp plugin new html-escape

  # 自述信息里用中文名，不要 fixture
  tokenmp plugin new html-escape --name "HTML 转义" --fixture=false

  # 覆盖已有的骨架
  tokenmp plugin new html-escape --force`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			key := args[0]
			if !keyPattern.MatchString(key) {
				return fmt.Errorf("插件 key %q 只能用小写字母、数字、下划线与短横线，且以字母或数字开头", key)
			}
			display := name
			if display == "" {
				display = key
			}

			env, err := pluginmgr.Load(opts.configPath, opts.dir)
			if err != nil {
				return err
			}

			dir := filepath.Join(env.Dir, key)
			pluginPath := filepath.Join(dir, plugin.PluginFile)
			fixturePath := filepath.Join(dir, "fixture.json")

			targets := []string{pluginPath}
			if withFixture {
				targets = append(targets, fixturePath)
			}
			if err := ensureAbsent(targets, force); err != nil {
				return err
			}

			if err := os.MkdirAll(dir, 0o755); err != nil {
				return fmt.Errorf("创建目录 %s 失败: %w", dir, err)
			}
			if err := os.WriteFile(pluginPath, []byte(fmt.Sprintf(pluginTemplate, key, display)), 0o644); err != nil {
				return fmt.Errorf("写入 %s 失败: %w", pluginPath, err)
			}
			if withFixture {
				if err := os.WriteFile(fixturePath, []byte(fixtureTemplate), 0o644); err != nil {
					return fmt.Errorf("写入 %s 失败: %w", fixturePath, err)
				}
			}

			writer := out(cmd)
			for _, target := range targets {
				fmt.Fprintf(writer, "%s %s\n", palette.ok.Render("已创建"), target)
			}
			fmt.Fprintf(writer, "\n%s\n", strings.Join([]string{
				fmt.Sprintf("  tokenmp plugin check %s", key),
				fmt.Sprintf("  tokenmp plugin replay %s", key),
				fmt.Sprintf("  tokenmp plugin enable %s", key),
			}, "\n"))
			return nil
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&name, "name", "", "插件展示名，默认与 key 相同")
	flags.BoolVar(&withFixture, "fixture", true, "同时生成 fixture 骨架")
	flags.BoolVar(&force, "force", false, "目标文件已存在时覆盖")
	return cmd
}

// ensureAbsent 确认目标文件都不存在，除非给出 force。
func ensureAbsent(targets []string, force bool) error {
	if force {
		return nil
	}
	for _, target := range targets {
		if _, err := os.Stat(target); err == nil {
			return fmt.Errorf("%s 已存在，改一个 key 或加 --force", target)
		}
	}
	return nil
}

package cmd

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/tokenmp/core/plugin"

	"github.com/tokenmp/cli/internal/pluginmgr"
)

// errReplayFailed 让回放命令以非零状态退出。用例明细已经打印过。
var errReplayFailed = errors.New("回放未通过，存在失败用例")

func newPluginReplayCmd(opts *pluginOptions) *cobra.Command {
	var fixturePath string

	cmd := &cobra.Command{
		Use:   "replay <key>",
		Short: "用 fixture 回放单个插件",
		Long: `用 fixture 回放单个插件。

fixture 的形状与 new-api 的任务插件一致：一组用例，每条给出钩子、参数、期望值或期望的错误子串。
回放把 host.now() 固定在 fixture 的 unixNow 上，时间戳与签名类钩子因此可复现。
钩子返回 null 表示不改写，回放按 null 比较。

有失败用例时以状态码 1 退出。`,
		Example: `  # 用插件旁边的 fixture
  tokenmp plugin replay echo

  # 指定另一份 fixture
  tokenmp plugin replay echo --fixture plugins/echo/fixture.json

  # 换一份配置，插件目录跟着配置走
  tokenmp plugin replay echo --config /etc/tokenmp/config.yaml`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			env, err := pluginmgr.Load(opts.configPath, opts.dir)
			if err != nil {
				return err
			}
			key := args[0]
			loaded, err := env.Find(key)
			if err != nil {
				return err
			}

			path := fixturePath
			if path == "" {
				path = defaultFixturePath(env.Dir, key)
			}
			fixture, err := loadFixture(path)
			if err != nil {
				return err
			}

			results := env.Bind(loaded).Replay(fixture, env.Logger)
			if opts.json {
				if err := printJSON(cmd, results); err != nil {
					return err
				}
			} else {
				renderResults(cmd, key, path, results)
			}

			if failures := countFailures(results); failures > 0 {
				return fmt.Errorf("%w: %d/%d 条用例失败", errReplayFailed, failures, len(results))
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&fixturePath, "fixture", "", "fixture 文件路径，默认取 <插件目录>/<key>/fixture.json")
	return cmd
}

// defaultFixturePath 是 fixture 的默认位置：插件目录里与插件同名的子目录。
func defaultFixturePath(dir, key string) string {
	return filepath.Join(dir, key, "fixture.json")
}

// loadFixture 读取并校验 fixture，缺失时给出默认位置。
func loadFixture(path string) (plugin.Fixture, error) {
	fixture, err := plugin.LoadFixture(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return plugin.Fixture{}, fmt.Errorf("没有找到 fixture %s，用 --fixture 指定路径", path)
		}
		return plugin.Fixture{}, err
	}
	if err := plugin.ValidateFixture(fixture); err != nil {
		return plugin.Fixture{}, err
	}
	return fixture, nil
}

// countFailures 数未通过的用例。
func countFailures(results []plugin.CaseResult) int {
	count := 0
	for _, result := range results {
		if !result.Passed {
			count++
		}
	}
	return count
}

// renderResults 打印回放结果与一行小结。
func renderResults(cmd *cobra.Command, key, path string, results []plugin.CaseResult) {
	writer := out(cmd)
	fmt.Fprintf(writer, "%s %s\n", palette.program.Render("replay"), palette.title.Render(key))
	fmt.Fprintf(writer, "%s %s\n\n", palette.label.Render("fixture:"), path)

	renderer := newTable().Headers("用例", "结果", "说明")
	for _, result := range results {
		renderer.Row(result.Name, resultLabel(result), dash(result.Message))
	}
	fmt.Fprintln(writer, renderer.Render())
	fmt.Fprintf(writer, "\n共 %d 条用例，%d 条通过\n", len(results), len(results)-countFailures(results))
}

// resultLabel 把用例结果渲染成带颜色的短词。
func resultLabel(result plugin.CaseResult) string {
	if result.Passed {
		return palette.ok.Render("通过")
	}
	return palette.fail.Render("失败")
}

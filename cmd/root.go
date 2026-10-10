// Package cmd 实现 tokenmp 命令行工具。
package cmd

import (
	"context"
	"os"
	"syscall"

	"github.com/charmbracelet/fang"
	"github.com/spf13/cobra"

	"github.com/tokenmp/cli/internal/version"
)

// Execute 组装命令树并运行。执行的命令报错时原样返回该错误，此时 fang 已经打印过
// 一份带样式的说明。
func Execute(ctx context.Context) error {
	info := version.Current()

	return fang.Execute(
		ctx,
		NewRootCmd(),
		fang.WithVersion(info.Version),
		fang.WithCommit(info.Commit),
		// 中文错误文案不需要 fang 默认处理器那套英文排版。
		fang.WithErrorHandler(errorHandler),
		// 中断信号接到命令的 context 上。run 靠它优雅退出；其余命令收到信号时
		// context 被取消，第二次信号按默认行为结束进程。
		fang.WithNotifySignal(os.Interrupt, syscall.SIGTERM),
		// 只发布 README 里列出的命令，cobra 自带的 completion 与 fang 自带的隐藏
		// man 命令因此关掉。去掉这两项即可恢复。
		fang.WithoutCompletions(),
		fang.WithoutManpage(),
	)
}

// NewRootCmd 组装整棵命令树。
func NewRootCmd() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "tokenmp",
		Short: "TokenMP 命令行工具",
		Long: `TokenMP 命令行工具。

完整命令列表用 ` + "`tokenmp help`" + `，单个命令的说明用 ` + "`tokenmp help <命令>`" + `。`,
		Example: `  # 打印本次构建的版本
  tokenmp version

  # 同一份信息的单行形式
  tokenmp --version

  # 查看插件目录里的插件与状态
  tokenmp plugin list

  # 看看还有哪些命令
  tokenmp help`,
		Args: cobra.NoArgs,
		// Version 是 --version/-v 报告的值；Execute 会用同一份构建元数据刷新它。
		Version: version.Current().String(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	rootCmd.AddCommand(newVersionCmd(), newRunCmd(), newPluginCmd(), newConfigCmd())
	localize(rootCmd)

	return rootCmd
}

// localize 把 cobra 自动生成的英文说明换成中文。
//
// cobra 自己加上 help 子命令、-h/--help 标志与 --version 标志，它们的说明文案是英文，
// 只有在这里统一改掉，否则帮助页面中英混排。已经写好的说明不会被覆盖。
func localize(cmd *cobra.Command) {
	cmd.InitDefaultHelpFlag()
	cmd.InitDefaultVersionFlag()
	cmd.InitDefaultHelpCmd()

	if flag := cmd.Flags().Lookup("help"); flag != nil {
		flag.Usage = "显示帮助"
	}
	if flag := cmd.Flags().Lookup("version"); flag != nil {
		flag.Usage = "显示版本"
	}

	for _, child := range cmd.Commands() {
		if child.Name() == "help" {
			child.Short = "显示任意命令的帮助"
			child.Long = "显示任意命令的帮助。\n\n用法：tokenmp help [命令路径]"
		}
		localize(child)
	}
}

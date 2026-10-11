package cmd

import (
	"errors"
	"fmt"

	"charm.land/huh/v2"
	"github.com/spf13/cobra"

	"github.com/tokenmp/cli/internal/configmgr"
)

// 交互菜单里的动作，也是选项上显示的文字。
const (
	manageCreate = "新建"
	manageEdit   = "编辑"
	manageDelete = "删除"
	manageQuit   = "退出"
)

// errManageCancelled 表示用户在当前动作里按 Esc 或 Ctrl-C 中断，回到菜单，不落盘。
var errManageCancelled = errors.New("已取消")

// manageItem 是交互列表里的一条记录：详情字段与选择时用的短标签。
type manageItem struct {
	detail [][2]string
	label  string
}

// manageList 描述一份可以分步管理的列表：条目怎么整理、删除怎么删。
//
// 列表本身统一打印与 list 子命令相同的表格，这里只放两者共用的整理逻辑。
type manageList struct {
	title string
	// items 从生效配置里整理出条目，顺序与 list 表一致。
	items func(env *configmgr.Env) []manageItem
	// remove 删除第 index 条，返回描述被删条目的文字。
	remove func(env *configmgr.Env, index int) (string, error)
}

// runManageForm 跑一个 huh 表单，输入输出接在命令自身的流上。
//
// 返回 false 表示用户中断（Esc 或 Ctrl-C），由调用方决定回到菜单还是直接退出；
// 其余错误原样返回。
func runManageForm(cmd *cobra.Command, form *huh.Form) (bool, error) {
	err := form.WithInput(cmd.InOrStdin()).WithOutput(cmd.OutOrStdout()).Run()
	switch {
	case errors.Is(err, huh.ErrUserAborted):
		return false, nil
	case err != nil:
		return false, err
	default:
		return true, nil
	}
}

// askManageAction 问接下来做什么。列表为空时只给新建与退出。
func askManageAction(cmd *cobra.Command, hasItems bool) (string, bool, error) {
	action := manageCreate
	options := []huh.Option[string]{huh.NewOption(manageCreate, manageCreate)}
	if hasItems {
		options = append(options,
			huh.NewOption(manageEdit, manageEdit),
			huh.NewOption(manageDelete, manageDelete),
		)
	}
	options = append(options, huh.NewOption(manageQuit, manageQuit))

	form := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().
			Title("要做什么？").
			Options(options...).
			Value(&action),
	))
	ok, err := runManageForm(cmd, form)
	return action, ok, err
}

// askManageIndex 从列表里选一条，返回下标。用户中断时 ok 为 false。
func askManageIndex(cmd *cobra.Command, list manageList, env *configmgr.Env) (int, bool, error) {
	items := list.items(env)
	options := make([]huh.Option[int], 0, len(items))
	for index, item := range items {
		options = append(options, huh.NewOption(item.label, index))
	}

	index := -1
	form := huh.NewForm(huh.NewGroup(
		huh.NewSelect[int]().
			Title("选一条" + list.title).
			Options(options...).
			Value(&index),
	))
	ok, err := runManageForm(cmd, form)
	if !ok {
		return -1, false, err
	}
	return index, true, err
}

// printManageDetail 打印选中条目的详情字段，让分步编辑前先看清当前值。
func printManageDetail(cmd *cobra.Command, list manageList, env *configmgr.Env, index int) error {
	items := list.items(env)
	if index < 0 || index >= len(items) {
		return fmt.Errorf("%s下标 %d 越界，共 %d 条", list.title, index, len(items))
	}
	writer := out(cmd)
	fmt.Fprintln(writer, palette.title.Render("当前"+list.title))
	printFields(writer, items[index].detail)
	return nil
}

// confirmManage 问一次是否落盘，供新建、编辑与删除共用。
func confirmManage(cmd *cobra.Command, question string) (bool, error) {
	confirmed := false
	form := huh.NewForm(huh.NewGroup(
		huh.NewConfirm().
			Title(question).
			Affirmative("写入").
			Negative("取消").
			Value(&confirmed),
	))
	ok, err := runManageForm(cmd, form)
	if !ok {
		return false, err
	}
	return confirmed, err
}

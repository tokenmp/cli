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
	manageView   = "查看"
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

// manageList 描述一份可以分步管理的列表。
//
// 表格由 table 现算，与 list 子命令共用同一份单元格；条目由 items 整理，顺序与下标
// 都与配置文件一致，改动按这个下标回写。
type manageList struct {
	title string
	// table 按当前配置画出列表表格，交互浏览与 list 子命令看到的是同一张表。
	table func(env *configmgr.Env) browseTable
	// items 从按文件原样读的配置里整理出条目，顺序与 list 表一致，下标也与文件一致。
	items func(env *configmgr.Env) []manageItem
	// create 走完分步表单追加一条；edit 与 remove 按下标改写、删除一条。
	create func(cmd *cobra.Command, env *configmgr.Env) error
	edit   func(cmd *cobra.Command, env *configmgr.Env, index int) error
	remove func(cmd *cobra.Command, env *configmgr.Env, index int) error
}

// manageKeyMap 是交互表单的按键表，在 huh 的默认表上把 Esc 也接成中断。
//
// huh 默认只认 Ctrl-C 中断，Esc 在动作菜单这类选择框上什么都不做（它的 Esc 绑定是
// 筛选用的，默认关着）。浏览态把 Esc 说成「退回去」的键，就得真的让它管用：接上
// 之后，每一个表单里按 Esc 都等于 Ctrl-C，取消当前这件、回到菜单、不落盘。
func manageKeyMap() *huh.KeyMap {
	keymap := huh.NewDefaultKeyMap()
	keymap.Quit.SetKeys("ctrl+c", "esc")
	keymap.Quit.SetHelp("esc", "cancel")
	return keymap
}

// runManageForm 跑一个 huh 表单，输入输出接在命令自身的流上。
//
// 返回 false 表示用户中断（Esc 或 Ctrl-C），由调用方决定回到菜单还是直接退出；
// 其余错误原样返回。
func runManageForm(cmd *cobra.Command, form *huh.Form) (bool, error) {
	err := form.WithKeyMap(manageKeyMap()).
		WithInput(cmd.InOrStdin()).
		WithOutput(cmd.OutOrStdout()).
		Run()
	switch {
	case errors.Is(err, huh.ErrUserAborted):
		return false, nil
	case err != nil:
		return false, err
	default:
		return true, nil
	}
}

// runManage 是分步管理的共用流程，channels、accounts 与 models 三个命令组走的是同一条。
//
// 每一轮先打印当前页的列表，再问做什么。浏览态（翻页、筛选、查看）只读，只有新建、
// 编辑、删除会落盘，且各自在写之前再确认一次。字段表单里按 Esc 或 Ctrl-C 只取消当前
// 这件，回到菜单；在菜单上按则退出。退出时文件不动。
func runManage(cmd *cobra.Command, configPath string, list manageList) error {
	session := browseSession{}
	for {
		env, err := configmgr.Load(configPath)
		if err != nil {
			return err
		}
		items := list.items(env)
		indexes := session.filter(items)
		printBrowsePage(cmd, list.table(env), session, indexes)

		action, ok, err := askBrowseAction(cmd, session, len(indexes))
		if err != nil {
			return err
		}
		if !ok || action == manageQuit {
			return nil
		}

		var actionErr error
		switch action {
		case manageCreate:
			actionErr = list.create(cmd, env)
		case browsePrev, browseNext:
			actionErr = turnPage(cmd, &session, action, len(indexes))
		case browseFind:
			keyword, ok, err := askBrowseKeyword(cmd)
			if err != nil {
				return err
			}
			if !ok {
				fmt.Fprintln(out(cmd), palette.label.Render("已取消"))
				continue
			}
			session.keyword = keyword
			// 换了筛选条件之后原页码多半越界，从头看起。
			session.page = 0
			continue
		case browseClear:
			session.keyword = ""
			session.page = 0
			continue
		case manageView, manageEdit, manageDelete:
			index, selected, err := askBrowseIndex(cmd, list, env, session)
			if err != nil {
				return err
			}
			if !selected {
				continue
			}
			// 详情没打出来（下标越界这类）时不再往下走动作：这时候改的是哪一条
			// 用户根本没看到。
			actionErr = printManageDetail(cmd, list, env, index)
			if actionErr == nil {
				switch action {
				case manageView:
					// 查看是只读的，看过就回到菜单。
				case manageEdit:
					actionErr = list.edit(cmd, env, index)
				case manageDelete:
					actionErr = list.remove(cmd, env, index)
				}
			}
		}

		switch {
		case errors.Is(actionErr, errManageCancelled):
			fmt.Fprintln(out(cmd), palette.label.Render("已取消"))
		case actionErr != nil:
			fmt.Fprintln(out(cmd), palette.fail.Render(actionErr.Error()))
		}
	}
}

// turnPage 翻页，到头或到尾时给出明确提示而不是静默不动。
func turnPage(cmd *cobra.Command, session *browseSession, action string, total int) error {
	pages := session.pageCount(total)
	switch action {
	case browsePrev:
		if session.page <= 0 {
			fmt.Fprintln(out(cmd), palette.warn.Render(fmt.Sprintf("已经是第一页（第 1 页 / 共 %d 页）", pages)))
			return nil
		}
		session.page--
	default:
		if session.page >= pages-1 {
			fmt.Fprintln(out(cmd), palette.warn.Render(fmt.Sprintf("已经是最后一页（第 %d 页 / 共 %d 页）", pages, pages)))
			return nil
		}
		session.page++
	}
	return nil
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

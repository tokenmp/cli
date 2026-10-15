package cmd

import (
	"fmt"
	"strings"

	"charm.land/huh/v2"
	"github.com/spf13/cobra"

	"github.com/tokenmp/cli/internal/configmgr"
)

// browsePageSize 是交互浏览里一页的条目数。
//
// 取 8 是为了让常见的 24 行终端放得下：表格本身占表头与分隔线，表下还要留出筛选
// 说明与动作菜单。这里不按终端高度现算，是因为列宽与换行会把行高变得不可预测，
// 算出来的页大小反而更难看；固定页大小配上页码，翻页的结果始终可预期。
const browsePageSize = 8

// browseTable 是一份列表的表格形态：表头、每一行的单元格与表下的补充说明。
//
// list 子命令与交互浏览都从这里取单元格，两边的列序与取值因此不会漂。
type browseTable struct {
	headers []string
	rows    [][]string
	// empty 是一条都没有时打印的提示，措辞与 list 子命令共用。
	empty string
	// note 按当前实际显示的那几个下标（在这一份 rows 里）给表下的补充说明，
	// 没有话要说时返回空串。
	//
	// 收成函数是因为交互浏览一次只显示一页：说明里点名的那几条如果不在这一页上，
	// 读的人会去表里找那几个名字，找不到。
	note func(visible []int) string
}

// render 画一张表格。空列表由调用方自己给提示，这里只画非空的部分。
func (t browseTable) render(visible []int) string {
	renderer := newTable().Headers(t.headers...)
	for _, index := range visible {
		renderer.Row(t.rows[index]...)
	}
	return renderer.Render()
}

// noteText 给出这批下标对应的表下说明，没有时说空串。
func (t browseTable) noteText(visible []int) string {
	if t.note == nil {
		return ""
	}
	return t.note(visible)
}

// all 是全部行的下标，list 子命令用它一次画完。
func (t browseTable) all() []int {
	indexes := make([]int, len(t.rows))
	for i := range t.rows {
		indexes[i] = i
	}
	return indexes
}

// browseSession 是交互浏览的状态：筛选关键字与页码。
//
// 状态跨轮保留，回到菜单再进来时还停在原来那一页，翻了半天不会白翻；
// 条目集合变了（新建或删除之后）由 pageCount 夹回合法范围。
type browseSession struct {
	keyword string
	page    int
}

// match 报告条目是否落在当前筛选里。筛选按条目的标签匹配，标签就是列表里那个名字。
func (s browseSession) match(item manageItem) bool {
	return s.keyword == "" || strings.Contains(item.label, s.keyword)
}

// filter 挑出落在筛选里的条目下标，顺序与列表一致。
func (s browseSession) filter(items []manageItem) []int {
	indexes := make([]int, 0, len(items))
	for index, item := range items {
		if s.match(item) {
			indexes = append(indexes, index)
		}
	}
	return indexes
}

// pageCount 报告筛完之后的页数，没有条目时算 0 页。
func (s browseSession) pageCount(total int) int {
	if total <= 0 {
		return 0
	}
	return (total + browsePageSize - 1) / browsePageSize
}

// pageRange 返回当前页在筛完的下标里的起止，页码越界时夹回合法范围。
func (s browseSession) pageRange(total int) (int, int) {
	pages := s.pageCount(total)
	if pages == 0 {
		return 0, 0
	}
	page := s.page
	if page < 0 {
		page = 0
	}
	if page >= pages {
		page = pages - 1
	}
	start := page * browsePageSize
	end := min(start+browsePageSize, total)
	return start, end
}

// browseHeading 是浏览表上方那行说明：正在筛什么、第几页共几页。
func (s browseSession) heading(total int) string {
	pages := s.pageCount(total)
	page := s.page + 1
	if pages == 0 {
		page = 0
	} else if page > pages {
		page = pages
	}
	parts := []string{fmt.Sprintf("第 %d 页 / 共 %d 页", page, pages), fmt.Sprintf("共 %d 条", total)}
	if s.keyword != "" {
		parts = append(parts, fmt.Sprintf("筛选：名字含 %q", s.keyword))
	}
	return strings.Join(parts, " · ")
}

// pageRows 从筛选结果的第 start 到 end 项映射成表格行的下标。
//
// 条目与表格行一一对应且同序，因此筛选结果里的位置直接就是表格行的下标。
func (s browseSession) pageRows(filtered []int) []int {
	start, end := s.pageRange(len(filtered))
	return filtered[start:end]
}

// printBrowsePage 打印当前页的条目表。
//
// 表格取 list 子命令的同一份单元格，只切出当前页那些行，所以浏览时看到的列与
// list 完全一致。表下的说明也只按这一页的行给出。
func printBrowsePage(cmd *cobra.Command, table browseTable, session browseSession, filtered []int) {
	writer := out(cmd)
	if len(filtered) == 0 {
		if session.keyword == "" {
			fmt.Fprintln(writer, table.empty)
		} else {
			fmt.Fprintf(writer, "%s\n", palette.warn.Render(
				fmt.Sprintf("没有名字含 %q 的条目，用「清除筛选」回到全量", session.keyword)))
		}
		return
	}

	visible := session.pageRows(filtered)
	fmt.Fprintln(writer, palette.title.Render(session.heading(len(filtered))))
	fmt.Fprintln(writer, table.render(visible))
	if note := table.noteText(visible); note != "" {
		fmt.Fprintf(writer, "\n%s\n", note)
	}
}

// browseAction 是浏览菜单里那些不选条目的动作。
const (
	browsePrev  = "上一页"
	browseNext  = "下一页"
	browseFind  = "按名称筛选"
	browseClear = "清除筛选"
)

// askBrowseAction 问接下来做什么，并把翻页与筛选的选项摆进去。
//
// 翻页选项只在真的有多页时出现；筛选取不到条目时给出清空筛选的入口，
// 否则筛选一旦生效就没有退路了。
func askBrowseAction(cmd *cobra.Command, session browseSession, total int) (string, bool, error) {
	pages := session.pageCount(total)
	options := []huh.Option[string]{huh.NewOption(manageCreate, manageCreate)}
	if total > 0 {
		options = append(options,
			huh.NewOption(manageView, manageView),
			huh.NewOption(manageEdit, manageEdit),
			huh.NewOption(manageDelete, manageDelete),
		)
	}
	if pages > 1 {
		options = append(options,
			huh.NewOption(browsePrev, browsePrev),
			huh.NewOption(browseNext, browseNext),
		)
	}
	if session.keyword == "" {
		options = append(options, huh.NewOption(browseFind, browseFind))
	} else {
		options = append(options, huh.NewOption(browseClear+"（当前 "+session.keyword+"）", browseClear))
	}
	options = append(options, huh.NewOption(manageQuit, manageQuit))

	action := manageCreate
	form := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().
			Title("要做什么？").
			Description(session.heading(total)).
			Options(options...).
			Value(&action),
	))
	ok, err := runManageForm(cmd, form)
	return action, ok, err
}

// askBrowseKeyword 问筛选关键字，空关键字表示清掉筛选。用户中断时 ok 为 false，筛选不变。
func askBrowseKeyword(cmd *cobra.Command) (string, bool, error) {
	keyword := ""
	form := huh.NewForm(huh.NewGroup(
		huh.NewInput().
			Title("只看名字含这个关键字的条目（留空表示不筛选）").
			Value(&keyword),
	))
	ok, err := runManageForm(cmd, form)
	if !ok {
		return "", false, err
	}
	return strings.TrimSpace(keyword), true, nil
}

// askBrowseIndex 从筛完的条目里选一条，返回它在配置里的下标。
//
// 选择范围是整个筛选结果，不只是当前页：翻页是给眼睛看的，选中哪一条不该被页码限制。
// 这里选中的下标是筛选之前的原始下标，调用方拿它去改配置不用再做一次映射。
func askBrowseIndex(cmd *cobra.Command, list manageList, env *configmgr.Env, session browseSession) (int, bool, error) {
	items := list.items(env)
	indexes := session.filter(items)
	options := make([]huh.Option[int], 0, len(indexes))
	for _, index := range indexes {
		options = append(options, huh.NewOption(items[index].label, index))
	}

	index := -1
	form := huh.NewForm(huh.NewGroup(
		huh.NewSelect[int]().
			Title("选一条" + list.title).
			Description(session.heading(len(indexes))).
			Options(options...).
			Value(&index),
	))
	ok, err := runManageForm(cmd, form)
	if !ok {
		return -1, false, err
	}
	return index, true, nil
}

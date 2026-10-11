package cmd

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/spf13/cobra"

	"github.com/tokenmp/core/config"

	"github.com/tokenmp/cli/internal/configmgr"
)

// browseItem 是交互界面里的一条记录：列表一行、详情字段与确认用的短标签。
type browseItem struct {
	// cells 是列表里的单元格，顺序与 browseList.header 对应。
	cells []string
	// detail 是详情字段，每项是标签与值。
	detail [][2]string
	// label 是确认与状态提示里指代这条记录的文字。
	label string
}

// browseList 描述一份可以浏览与删除的列表，channels 与 models 各填一份。
type browseList struct {
	title  string
	header []string
	// reload 从配置文件重新读一遍，返回最新条目。
	reload func() ([]browseItem, error)
	// remove 删除第 index 条，返回描述被删条目的文字。
	remove func(index int) (string, error)
}

// browseStatus 是状态栏的语气，决定用哪一档颜色。
type browseStatus int

const (
	browseInfo browseStatus = iota
	browseSuccess
	browseWarning
	browseFailure
)

// browser 是浏览与删除的交互模型。
//
// 更新函数只改内存状态，写文件全部走 browseList.remove，因此取消或退出都不会落盘。
type browser struct {
	list  browseList
	items []browseItem

	cursor int
	offset int

	width  int
	height int

	confirm bool
	status  string
	kind    browseStatus

	quit bool
}

// newBrowser 读一遍列表并返回交互模型，读失败时把错误原样返回。
func newBrowser(list browseList) (*browser, error) {
	model := &browser{list: list}
	if err := model.reload(); err != nil {
		return nil, err
	}
	return model, nil
}

// Init 不执行初始命令。
func (m *browser) Init() tea.Cmd { return nil }

// Update 处理按键与终端尺寸消息。
func (m *browser) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.clampWindow()
		return m, nil
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

// View 渲染界面，并声明使用备用屏幕。
func (m *browser) View() tea.View {
	view := tea.NewView(m.render())
	view.AltScreen = true
	return view
}

// handleKey 按当前状态分派按键。
func (m *browser) handleKey(key tea.KeyMsg) (*browser, tea.Cmd) {
	switch key.String() {
	case "q", "ctrl+c":
		m.quit = true
		return m, tea.Quit
	}

	if m.confirm {
		switch key.String() {
		case "y", "Y", "enter":
			m.deleteSelected()
		default:
			m.confirm = false
			m.status = "已取消删除"
			m.kind = browseInfo
		}
		return m, nil
	}

	switch key.String() {
	case "up", "k":
		m.moveCursor(-1)
	case "down", "j":
		m.moveCursor(1)
	case "pgup", "ctrl+u":
		m.moveCursor(-m.pageSize())
	case "pgdown", "ctrl+d":
		m.moveCursor(m.pageSize())
	case "home", "g":
		m.moveTo(0)
	case "end", "G":
		m.moveTo(len(m.items) - 1)
	case "d", "delete":
		m.askDelete()
	}
	return m, nil
}

// askDelete 进入删除确认状态，确认前不碰文件。
func (m *browser) askDelete() {
	if len(m.items) == 0 {
		return
	}
	m.confirm = true
	m.status = fmt.Sprintf("删除 %s？按 y 确认，其余键取消", m.items[m.cursor].label)
	m.kind = browseWarning
}

// deleteSelected 删除选中条目，成功后刷新列表并报告删掉了哪一条。
func (m *browser) deleteSelected() {
	index := m.cursor
	m.confirm = false
	if index < 0 || index >= len(m.items) {
		m.status = "没有可以删除的条目"
		m.kind = browseWarning
		return
	}

	label := m.items[index].label
	removed, err := m.list.remove(index)
	if err != nil {
		m.status = err.Error()
		m.kind = browseFailure
		return
	}
	if removed == "" {
		removed = label
	}
	if err := m.reload(); err != nil {
		m.status = err.Error()
		m.kind = browseFailure
		return
	}
	m.status = "已删除 " + removed
	m.kind = browseSuccess
}

// reload 重新读一遍列表，并把游标收进新的范围。
func (m *browser) reload() error {
	items, err := m.list.reload()
	if err != nil {
		return err
	}
	m.items = items
	m.confirm = false
	m.clampWindow()
	return nil
}

// moveCursor 相对移动游标。
func (m *browser) moveCursor(delta int) {
	m.moveTo(m.cursor + delta)
}

// moveTo 把游标移到指定位置，并收进合法范围。
func (m *browser) moveTo(index int) {
	m.cursor = index
	m.clampWindow()
}

// clampWindow 把游标限制在条目范围内，并滚动窗口让游标可见。
func (m *browser) clampWindow() {
	total := len(m.items)
	if total == 0 {
		m.cursor, m.offset = 0, 0
		return
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= total {
		m.cursor = total - 1
	}

	page := m.pageSize()
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+page {
		m.offset = m.cursor - page + 1
	}
	if m.offset < 0 {
		m.offset = 0
	}
	if m.offset > total-1 {
		m.offset = total - 1
	}
}

// pageSize 是列表一次显示的行数，给详情与提示留出空间。
func (m *browser) pageSize() int {
	if m.height <= 0 {
		return 8
	}
	size := (m.height - 6) / 2
	if size < 1 {
		size = 1
	}
	return size
}

// render 把标题、列表、详情与状态拼成界面文本。
func (m *browser) render() string {
	width := m.width
	if width <= 0 {
		width = headerWidth
	}

	lines := []string{palette.title.Render(m.list.title)}

	if len(m.items) == 0 {
		lines = append(lines, palette.label.Render("这里没有条目"))
	} else {
		widths := m.columnWidths()
		lines = append(lines, palette.title.Render(formatCells(m.list.header, widths)))

		page := m.pageSize()
		end := m.offset + page
		if end > len(m.items) {
			end = len(m.items)
		}
		for i := m.offset; i < end; i++ {
			marker := "  "
			if i == m.cursor {
				marker = palette.program.Render("›") + " "
			}
			lines = append(lines, ansi.Truncate(marker+formatCells(m.items[i].cells, widths), width, "…"))
		}
		if len(m.items) > page {
			lines = append(lines, palette.label.Render(fmt.Sprintf("第 %d-%d 条，共 %d 条", m.offset+1, end, len(m.items))))
		}

		lines = append(lines, "")
		lines = append(lines, m.detailLines(width)...)
	}

	if m.status != "" {
		lines = append(lines, "")
		lines = append(lines, ansi.Truncate(m.statusStyle().Render(m.status), width, "…"))
	}
	lines = append(lines, palette.label.Render("q 退出 · ↑/↓ 移动 · PgUp/PgDn 翻页 · d 删除"))

	return strings.Join(clipLines(lines, m.height), "\n") + "\n"
}

// detailLines 渲染选中条目的详情字段。
func (m *browser) detailLines(width int) []string {
	if m.cursor < 0 || m.cursor >= len(m.items) {
		return nil
	}
	detail := m.items[m.cursor].detail
	if len(detail) == 0 {
		return nil
	}

	labelWidth := 0
	for _, field := range detail {
		if w := lipgloss.Width(field[0]) + 1; w > labelWidth {
			labelWidth = w
		}
	}

	lines := []string{palette.title.Render("详情")}
	for _, field := range detail {
		label := palette.label.Width(labelWidth).Render(field[0] + ":")
		lines = append(lines, ansi.Truncate(label+" "+palette.cell.Render(field[1]), width, "…"))
	}
	return lines
}

// statusStyle 按状态语气取颜色。
func (m *browser) statusStyle() lipgloss.Style {
	switch m.kind {
	case browseSuccess:
		return palette.ok
	case browseWarning:
		return palette.warn
	case browseFailure:
		return palette.fail
	default:
		return palette.label
	}
}

// columnWidths 按表头与全部条目算出每列宽度，窗口滚动时列宽保持稳定。
func (m *browser) columnWidths() []int {
	widths := make([]int, len(m.list.header))
	for i, head := range m.list.header {
		widths[i] = lipgloss.Width(head)
	}
	for _, item := range m.items {
		for i, cell := range item.cells {
			if i >= len(widths) {
				continue
			}
			if w := lipgloss.Width(cell); w > widths[i] {
				widths[i] = w
			}
		}
	}
	return widths
}

// formatCells 按列宽把单元格补齐并用两个空格分开。
func formatCells(values []string, widths []int) string {
	parts := make([]string, 0, len(values))
	for i, value := range values {
		if i >= len(widths) {
			parts = append(parts, value)
			continue
		}
		padding := widths[i] - lipgloss.Width(value)
		if padding < 0 {
			padding = 0
		}
		parts = append(parts, value+strings.Repeat(" ", padding))
	}
	return strings.Join(parts, "  ")
}

// clipLines 把行数限制在终端高度内，高度未知时原样返回。
func clipLines(lines []string, height int) []string {
	if height <= 0 || len(lines) <= height {
		return lines
	}
	return lines[:height]
}

// runBrowse 启动交互界面，直到用户退出。
//
// 中断信号也会结束界面，此时按正常退出处理，不写文件，也不当成错误。
func runBrowse(cmd *cobra.Command, list browseList) error {
	model, err := newBrowser(list)
	if err != nil {
		return err
	}

	program := tea.NewProgram(model, tea.WithOutput(cmd.OutOrStdout()))
	if _, err := program.Run(); err != nil && !errors.Is(err, tea.ErrInterrupted) {
		return err
	}
	return nil
}

// channelBrowseList 组装渠道的浏览列表：列表列与 channels list 一致，详情是九个字段。
func channelBrowseList(configPath string) browseList {
	return browseList{
		title:  "渠道",
		header: []string{"渠道", "方言", "基址", "路径", "鉴权", "凭据", "超时", "重试"},
		reload: func() ([]browseItem, error) {
			env, err := configmgr.Load(configPath)
			if err != nil {
				return nil, err
			}
			return channelBrowseItems(env), nil
		},
		remove: func(index int) (string, error) {
			env, err := configmgr.Load(configPath)
			if err != nil {
				return "", err
			}
			if index < 0 || index >= len(env.Config.Channels) {
				return "", fmt.Errorf("渠道下标 %d 越界，共 %d 条", index, len(env.Config.Channels))
			}
			name := env.Config.Channels[index].Name
			if _, err := removeChannelAt(env, index); err != nil {
				return "", err
			}
			return name, nil
		},
	}
}

// channelBrowseItems 把渠道整理成浏览条目。
func channelBrowseItems(env *configmgr.Env) []browseItem {
	rows := channelRows(env.Config)
	items := make([]browseItem, 0, len(rows))
	for index, row := range rows {
		items = append(items, browseItem{
			cells: []string{
				row.Name,
				row.Dialect,
				row.BaseURL,
				dash(row.Path),
				row.Auth,
				credentialText(row.Credential),
				row.Timeout,
				fmt.Sprintf("%d", row.MaxRetries),
			},
			detail: channelDetail(env.Config.Channels[index], row),
			label:  row.Name,
		})
	}
	return items
}

// channelDetail 是渠道的九个详情字段。
func channelDetail(record config.Channel, row channelRow) [][2]string {
	return [][2]string{
		{"渠道", row.Name},
		{"方言", row.Dialect},
		{"基址", row.BaseURL},
		{"路径", dash(row.Path)},
		{"鉴权", row.Auth},
		{"凭据", row.Credential},
		{"超时", row.Timeout},
		{"最大重试", fmt.Sprintf("%d", row.MaxRetries)},
		{"请求头", headerNames(record.Headers)},
	}
}

// headerNames 把自定义请求头的名字排序后连成一行，没有时显示占位符。
func headerNames(headers map[string]string) string {
	if len(headers) == 0 {
		return "-"
	}
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, "、")
}

// modelBrowseList 组装模型规则的浏览列表：列表列与 models list 一致，详情是五个字段。
func modelBrowseList(configPath string) browseList {
	return browseList{
		title:  "模型路由规则",
		header: []string{"序号", "匹配", "渠道", "上游模型", "方言", "亲和"},
		reload: func() ([]browseItem, error) {
			env, err := configmgr.Load(configPath)
			if err != nil {
				return nil, err
			}
			return modelBrowseItems(env), nil
		},
		remove: func(index int) (string, error) {
			env, err := configmgr.Load(configPath)
			if err != nil {
				return "", err
			}
			if index < 0 || index >= len(env.Config.Models) {
				return "", fmt.Errorf("规则下标 %d 越界，共 %d 条", index, len(env.Config.Models))
			}
			rule := env.Config.Models[index]
			if _, err := removeModelAt(env, index); err != nil {
				return "", err
			}
			return fmt.Sprintf("models.%d（match %s → %s）", index, rule.Match, rule.Channel), nil
		},
	}
}

// modelBrowseItems 把模型规则整理成浏览条目。
func modelBrowseItems(env *configmgr.Env) []browseItem {
	rows := ruleRows(env.Config)
	items := make([]browseItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, browseItem{
			cells: []string{
				fmt.Sprintf("%d", row.Index),
				row.Match,
				row.Channel,
				upstreamText(row.Upstream),
				row.Dialect,
				dash(row.Affinity),
			},
			detail: [][2]string{
				{"匹配", row.Match},
				{"渠道", row.Channel},
				{"上游模型", upstreamText(row.Upstream)},
				{"方言", row.Dialect},
				{"亲和", dash(row.Affinity)},
			},
			label: fmt.Sprintf("models.%d（match %s）", row.Index, row.Match),
		})
	}
	return items
}

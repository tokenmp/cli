package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"
)

// browseConfig 有两条渠道与两条规则：primary 被两条规则引用，backup 没有被引用。
const browseConfig = `# 沙箱配置
listen: 127.0.0.1:9999

log:
  format: text

channels:
  # 主渠道，被规则引用
  - name: primary
    dialect: openai
    base_url: https://api.example.com/v1
    auth:
      style: bearer
      key_env: PRIMARY_KEY

  # 备用渠道，没有规则引用
  - name: backup
    dialect: claude
    base_url: https://api.backup.com
    auth:
      style: none

models:
  # 主规则
  - match: "gpt-*"
    channel: primary
  - match: "claude-*"
    channel: primary
`

// runeKey 造一个可打印字符的按键。
func runeKey(r rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: r, Text: string(r)}
}

// specialKey 造一个特殊键的按键，例如 tea.KeyDown。
func specialKey(code rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: code}
}

// pressKey 把按键交给模型并取回更新后的模型。
func pressKey(t *testing.T, model *browser, key tea.KeyPressMsg) *browser {
	t.Helper()

	updated, _ := model.Update(key)
	next, ok := updated.(*browser)
	if !ok {
		t.Fatalf("Update 返回 %T，想要 *browser", updated)
	}
	return next
}

// TestChannelsGroupFallsBackToTable 验证非终端下 channels 组与 channels list 输出一致。
func TestChannelsGroupFallsBackToTable(t *testing.T) {
	path := configSandbox(t, browseConfig)

	group, err := run(t, "channels", "--config", path)
	if err != nil {
		t.Fatalf("channels: %v\n%s", err, group)
	}
	list, err := run(t, "channels", "list", "--config", path)
	if err != nil {
		t.Fatalf("channels list: %v\n%s", err, list)
	}
	if group != list {
		t.Errorf("非终端时 channels 应当与 channels list 一致\nchannels:\n%s\nlist:\n%s", group, list)
	}
	for _, want := range []string{"primary", "backup", "凭据环境变量没有设置"} {
		if !strings.Contains(group, want) {
			t.Errorf("表格里没有 %q:\n%s", want, group)
		}
	}
}

// TestModelsGroupFallsBackToTable 验证非终端下 models 组与 models list 输出一致。
func TestModelsGroupFallsBackToTable(t *testing.T) {
	path := configSandbox(t, browseConfig)

	group, err := run(t, "models", "--config", path)
	if err != nil {
		t.Fatalf("models: %v\n%s", err, group)
	}
	list, err := run(t, "models", "list", "--config", path)
	if err != nil {
		t.Fatalf("models list: %v\n%s", err, list)
	}
	if group != list {
		t.Errorf("非终端时 models 应当与 models list 一致\nmodels:\n%s\nlist:\n%s", group, list)
	}
	for _, want := range []string{"gpt-*", "claude-*", "primary"} {
		if !strings.Contains(group, want) {
			t.Errorf("表格里没有 %q:\n%s", want, group)
		}
	}
}

// TestChannelsGroupJSONNonTTY 验证管道里给 --json 打印 JSON，而不是报互斥错误。
func TestChannelsGroupJSONNonTTY(t *testing.T) {
	path := configSandbox(t, browseConfig)

	group, err := run(t, "channels", "--config", path, "--json")
	if err != nil {
		t.Fatalf("channels --json: %v\n%s", err, group)
	}
	list, err := run(t, "channels", "list", "--config", path, "--json")
	if err != nil {
		t.Fatalf("channels list --json: %v\n%s", err, list)
	}
	if group != list {
		t.Errorf("两处 --json 输出应当一致\nchannels:\n%s\nlist:\n%s", group, list)
	}

	var rows []channelRow
	if err := json.Unmarshal([]byte(group), &rows); err != nil {
		t.Fatalf("输出不是 JSON 数组: %v\n%s", err, group)
	}
	if len(rows) != 2 {
		t.Errorf("JSON 里有 %d 条渠道，想要 2 条", len(rows))
	}
}

// TestModelsGroupJSONNonTTY 验证 models 组在管道里的 --json 行为。
func TestModelsGroupJSONNonTTY(t *testing.T) {
	path := configSandbox(t, browseConfig)

	group, err := run(t, "models", "--config", path, "--json")
	if err != nil {
		t.Fatalf("models --json: %v\n%s", err, group)
	}
	list, err := run(t, "models", "list", "--config", path, "--json")
	if err != nil {
		t.Fatalf("models list --json: %v\n%s", err, list)
	}
	if group != list {
		t.Errorf("两处 --json 输出应当一致\nmodels:\n%s\nlist:\n%s", group, list)
	}

	var output modelsOutput
	if err := json.Unmarshal([]byte(group), &output); err != nil {
		t.Fatalf("输出不是 JSON: %v\n%s", err, group)
	}
	if len(output.Rules) != 2 {
		t.Errorf("JSON 里有 %d 条规则，想要 2 条", len(output.Rules))
	}
}

// TestGroupJSONConflictsWithInteractive 验证进交互时 --json 报错。
//
// interactive 是 runChannels 与 runModels 的参数，测试直接给 true，不必有真的终端。
func TestGroupJSONConflictsWithInteractive(t *testing.T) {
	path := configSandbox(t, browseConfig)

	if err := runChannels(&cobra.Command{}, &channelsOptions{configPath: path, jsonOut: true}, true); err == nil {
		t.Error("channels 进交互时给 --json 应当报错")
	} else if !strings.Contains(err.Error(), "--json") {
		t.Errorf("错误信息里应当点出 --json: %v", err)
	}

	if err := runModels(&cobra.Command{}, &modelsOptions{configPath: path, jsonOut: true}, true); err == nil {
		t.Error("models 进交互时给 --json 应当报错")
	} else if !strings.Contains(err.Error(), "--json") {
		t.Errorf("错误信息里应当点出 --json: %v", err)
	}
}

// TestChannelsBrowseDeleteKeepsComments 验证交互删除后文件里少一条，其余注释保留。
func TestChannelsBrowseDeleteKeepsComments(t *testing.T) {
	path := configSandbox(t, browseConfig)

	model, err := newBrowser(channelBrowseList(path))
	if err != nil {
		t.Fatalf("打不开浏览界面: %v", err)
	}
	if len(model.items) != 2 {
		t.Fatalf("初始有 %d 条渠道，想要 2 条", len(model.items))
	}

	// 移到备用渠道。
	model = pressKey(t, model, specialKey(tea.KeyDown))
	if model.cursor != 1 {
		t.Fatalf("游标在 %d，想要 1", model.cursor)
	}

	before := readFile(t, path)
	model = pressKey(t, model, runeKey('d'))
	if !model.confirm {
		t.Fatal("按 d 后应当进入删除确认")
	}
	if readFile(t, path) != before {
		t.Fatal("确认前不该改文件")
	}

	model = pressKey(t, model, runeKey('y'))
	if model.confirm {
		t.Error("确认后应当退出确认状态")
	}
	if model.kind != browseSuccess {
		t.Errorf("删除成功后状态应当是成功，实际是 %v: %s", model.kind, model.status)
	}
	if !strings.Contains(model.status, "已删除") || !strings.Contains(model.status, "backup") {
		t.Errorf("状态里应当写明删掉了哪一条: %s", model.status)
	}
	if !strings.Contains(model.View().Content, "已删除") {
		t.Error("界面上应当显示删除结果")
	}
	if len(model.items) != 1 {
		t.Errorf("删除后应当刷新成 1 条，实际 %d 条", len(model.items))
	}

	data := readFile(t, path)
	if strings.Contains(data, "backup") {
		t.Errorf("文件里不该还有 backup:\n%s", data)
	}
	for _, want := range []string{"# 主渠道", "# 主规则", "- name: primary", "gpt-*"} {
		if !strings.Contains(data, want) {
			t.Errorf("改写结果里没有 %q:\n%s", want, data)
		}
	}
}

// TestChannelsBrowseRefusesReferenced 验证交互路径同样拒绝删除被引用的渠道。
func TestChannelsBrowseRefusesReferenced(t *testing.T) {
	path := configSandbox(t, browseConfig)

	model, err := newBrowser(channelBrowseList(path))
	if err != nil {
		t.Fatalf("打不开浏览界面: %v", err)
	}
	before := readFile(t, path)

	// 第一行是 primary，被两条规则引用。
	model = pressKey(t, model, runeKey('d'))
	model = pressKey(t, model, runeKey('y'))

	if model.kind != browseFailure {
		t.Errorf("删除被引用的渠道应当失败，状态是 %v: %s", model.kind, model.status)
	}
	for _, want := range []string{"仍被", "models[0]", "models[1]", "gpt-*"} {
		if !strings.Contains(model.status, want) {
			t.Errorf("拒绝信息里应当包含 %q: %s", want, model.status)
		}
	}
	if len(model.items) != 2 {
		t.Errorf("被拒绝后列表不该变，实际 %d 条", len(model.items))
	}
	if readFile(t, path) != before {
		t.Error("被拒绝后文件不该改动")
	}
}

// TestBrowseCancelDoesNotWrite 验证取消与退出都不落盘。
func TestBrowseCancelDoesNotWrite(t *testing.T) {
	path := configSandbox(t, browseConfig)
	before := readFile(t, path)

	model, err := newBrowser(channelBrowseList(path))
	if err != nil {
		t.Fatalf("打不开浏览界面: %v", err)
	}

	model = pressKey(t, model, runeKey('d'))
	model = pressKey(t, model, runeKey('n'))
	if model.confirm {
		t.Error("按 n 后应当退出确认状态")
	}
	if model.kind != browseInfo {
		t.Errorf("取消后状态应当是提示，实际是 %v", model.kind)
	}
	if readFile(t, path) != before {
		t.Fatal("取消删除后文件不该改动")
	}

	model = pressKey(t, model, runeKey('q'))
	if !model.quit {
		t.Error("按 q 后应当标记退出")
	}
	if readFile(t, path) != before {
		t.Error("退出后文件不该改动")
	}
}

// TestModelsBrowseDelete 验证交互删除一条规则。
func TestModelsBrowseDelete(t *testing.T) {
	path := configSandbox(t, browseConfig)

	model, err := newBrowser(modelBrowseList(path))
	if err != nil {
		t.Fatalf("打不开浏览界面: %v", err)
	}

	model = pressKey(t, model, specialKey(tea.KeyDown))
	model = pressKey(t, model, runeKey('d'))
	model = pressKey(t, model, runeKey('y'))

	if model.kind != browseSuccess {
		t.Fatalf("删除规则应当成功，状态是 %v: %s", model.kind, model.status)
	}
	if !strings.Contains(model.status, "models.1") || !strings.Contains(model.status, "claude-*") {
		t.Errorf("状态里应当写明删掉的规则: %s", model.status)
	}
	if len(model.items) != 1 {
		t.Errorf("删除后应当剩 1 条，实际 %d 条", len(model.items))
	}

	data := readFile(t, path)
	if strings.Contains(data, "claude-*") {
		t.Errorf("文件里不该还有 claude-*:\n%s", data)
	}
	for _, want := range []string{"# 主规则", "gpt-*", "- name: primary"} {
		if !strings.Contains(data, want) {
			t.Errorf("改写结果里没有 %q:\n%s", want, data)
		}
	}
}

// TestBrowseNavigationAndPaging 验证移动、翻页与窗口滚动。
func TestBrowseNavigationAndPaging(t *testing.T) {
	model, err := newBrowser(fakeBrowseList(30))
	if err != nil {
		t.Fatalf("打不开浏览界面: %v", err)
	}
	model.Update(tea.WindowSizeMsg{Width: 100, Height: 24})

	page := model.pageSize()
	if page != 9 {
		t.Fatalf("一页应当是 9 条，实际 %d 条", page)
	}

	model = pressKey(t, model, specialKey(tea.KeyPgDown))
	if model.cursor != page {
		t.Errorf("翻页后游标在 %d，想要 %d", model.cursor, page)
	}
	if model.offset != 1 {
		t.Errorf("翻页后窗口起点在 %d，想要 1", model.offset)
	}

	model = pressKey(t, model, specialKey(tea.KeyEnd))
	if model.cursor != 29 {
		t.Errorf("End 后游标在 %d，想要 29", model.cursor)
	}
	if model.offset+page <= 29 {
		t.Errorf("End 后窗口没有滚到底: 起点 %d，一页 %d", model.offset, page)
	}

	model = pressKey(t, model, specialKey(tea.KeyHome))
	if model.cursor != 0 || model.offset != 0 {
		t.Errorf("Home 后应当是 0/0，实际 %d/%d", model.cursor, model.offset)
	}
}

// TestBrowseDetails 验证详情字段的数量与标签。
func TestBrowseDetails(t *testing.T) {
	path := configSandbox(t, browseConfig)

	channels, err := newBrowser(channelBrowseList(path))
	if err != nil {
		t.Fatalf("打不开渠道界面: %v", err)
	}
	if len(channels.items[0].detail) != 9 {
		t.Errorf("渠道详情有 %d 个字段，想要 9 个", len(channels.items[0].detail))
	}
	for _, want := range []string{"渠道:", "方言:", "基址:", "路径:", "鉴权:", "凭据:", "超时:", "最大重试:", "请求头:"} {
		if !strings.Contains(channels.View().Content, want) {
			t.Errorf("渠道详情里没有 %q:\n%s", want, channels.View().Content)
		}
	}

	rules, err := newBrowser(modelBrowseList(path))
	if err != nil {
		t.Fatalf("打不开规则界面: %v", err)
	}
	if len(rules.items[0].detail) != 5 {
		t.Errorf("规则详情有 %d 个字段，想要 5 个", len(rules.items[0].detail))
	}
	for _, want := range []string{"匹配:", "渠道:", "上游模型:", "方言:", "亲和:"} {
		if !strings.Contains(rules.View().Content, want) {
			t.Errorf("规则详情里没有 %q:\n%s", want, rules.View().Content)
		}
	}
}

// TestGroupHelpStillWorks 验证 --help 与 help 仍然给帮助，不被交互挡住。
func TestGroupHelpStillWorks(t *testing.T) {
	for _, args := range [][]string{{"channels", "--help"}, {"help", "channels"}, {"models", "--help"}, {"help", "models"}} {
		out, err := run(t, args...)
		if err != nil {
			t.Fatalf("tokenmp %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		if !strings.Contains(out, "交互界面") {
			t.Errorf("tokenmp %s 的帮助里应当说明交互界面:\n%s", strings.Join(args, " "), out)
		}
	}
}

// fakeBrowseList 造一份只在内存里的列表，用来测移动与翻页。
func fakeBrowseList(count int) browseList {
	return browseList{
		title:  "测试",
		header: []string{"序号", "名称"},
		reload: func() ([]browseItem, error) {
			items := make([]browseItem, 0, count)
			for i := 0; i < count; i++ {
				items = append(items, browseItem{
					cells:  []string{fmt.Sprintf("%d", i), fmt.Sprintf("条目 %d", i)},
					detail: [][2]string{{"序号", fmt.Sprintf("%d", i)}},
					label:  fmt.Sprintf("第 %d 条", i),
				})
			}
			return items, nil
		},
		remove: func(int) (string, error) { return "", nil },
	}
}

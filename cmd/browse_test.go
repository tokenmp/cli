package cmd

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// browseCount 是浏览态测试的条目数，取 12 是为了让它跨两页（每页 8 条）。
const browseCount = 12

// scriptedInput 按行喂给交互表单，一次 Read 只交出一行。
//
// huh 的应答模式每条提示都新起一个 bufio.Scanner，Scanner 读到换行就收手，
// 因此这里一次给一行，第几个表单就吃到第几行，不会串。
type scriptedInput struct {
	lines []string
}

func (r *scriptedInput) Read(p []byte) (int, error) {
	if len(r.lines) == 0 {
		return 0, io.EOF
	}
	line := r.lines[0] + "\n"
	if len(line) > len(p) {
		r.lines[0] = line[len(p):]
		return copy(p, line[:len(p)]), nil
	}
	r.lines = r.lines[1:]
	return copy(p, line), nil
}

// browseSessionRun 驱动一次分步管理流程，返回去掉颜色码之后的全部输出。
//
// TERM=dumb 会让 huh 走应答模式：把选项编号印成一行行文字、从 stdin 读编号，
// 于是分步流程在没有终端的测试里也能真跑一遍，而不是只调几个纯函数。
// 颜色码去掉之后再断言，断言看的是文字，不该被配色干扰。
func browseSessionRun(t *testing.T, configPath string, run func(*cobra.Command, string) error, lines ...string) string {
	t.Helper()
	t.Setenv("TERM", "dumb")

	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetIn(&scriptedInput{lines: lines})

	if err := run(cmd, configPath); err != nil {
		t.Fatalf("分步管理流程出错: %v\n%s", err, plainText(buf.String()))
	}
	return plainText(buf.String())
}

// plainText 去掉 ANSI 转义序列，只留下看得见的文字。
func plainText(text string) string {
	var b strings.Builder
	for i := 0; i < len(text); {
		if text[i] == 0x1b && i+1 < len(text) && text[i+1] == '[' {
			i += 2
			for i < len(text) && (text[i] < 0x40 || text[i] > 0x7e) {
				i++
			}
			i++
			continue
		}
		b.WriteByte(text[i])
		i++
	}
	return b.String()
}

// browseRoundMark 是每一轮菜单末尾的选项提示，也是把输出切成一轮一轮的锚点。
const browseRoundMark = "Enter a number between"

// browseRounds 把一次交互的输出切成一轮一轮。
//
// 切点取选项提示行：每一轮菜单末尾都有一处。切出来每一块是「这一轮打印的表格 +
// 这一轮摆出的菜单」，前面还带着上一轮那个动作的结果（翻到头、筛不到这类提示）。
// 于是断言只需按轮取下标，不必去数字符偏移，也不会把别的轮的内容算进来。
//
// 挑一条时那个下拉框也带同样的提示行，会多切出一块，下标因此按实际布局写。
func browseRounds(text string) []string {
	parts := strings.Split(text, browseRoundMark)
	rounds := []string{parts[0]}
	for _, part := range parts[1:] {
		// 提示行的尾巴「1 and 8: 」跟下一轮的内容无关，切掉。
		if newline := strings.IndexByte(part, '\n'); newline >= 0 {
			part = part[newline+1:]
		}
		rounds = append(rounds, part)
	}
	return rounds
}

// round 取第 index 块，越界时直接失败。
func round(t *testing.T, out string, index int) string {
	t.Helper()
	rounds := browseRounds(out)
	if index >= len(rounds) {
		t.Fatalf("输出只有 %d 轮，取不到第 %d 轮:\n%s", len(rounds), index, out)
	}
	return rounds[index]
}

// detailBlock 截出详情那一段：从详情标题到下一张表格的表头之前。
func detailBlock(t *testing.T, text, title string) string {
	t.Helper()
	start := strings.Index(text, title)
	if start < 0 {
		t.Fatalf("输出里没有 %q:\n%s", title, text)
	}
	rest := text[start:]
	if end := strings.Index(rest, "\n第 "); end >= 0 {
		return rest[:end]
	}
	return rest
}

// browseManyConfig 造一份渠道数为 count 的配置：前 8 条叫 svc-NN，其余叫 alpha-NN。
//
// 两种前缀把「翻页」与「筛选」分开验证：全量跨两页，筛 alpha 之后只剩一页里的 4 条。
func browseManyConfig(count int) string {
	var b strings.Builder
	b.WriteString("listen: 127.0.0.1:9999\n\nchannels:\n")
	names := make([]string, 0, count)
	for i := 1; i <= count; i++ {
		name := fmt.Sprintf("svc-%02d", i)
		if i > browsePageSize {
			name = fmt.Sprintf("alpha-%02d", i-browsePageSize)
		}
		names = append(names, name)
		fmt.Fprintf(&b, "  - name: %s\n    dialect: openai\n    base_url: https://api.example.com/%02d\n    auth:\n      style: none\n", name, i)
	}
	b.WriteString("\nmodels:\n")
	for _, name := range names {
		fmt.Fprintf(&b, "  - match: \"gpt-*\"\n    channel: %s\n", name)
	}
	return b.String()
}

// TestBrowsePagingAndBoundaries 验证翻页、页码说明与翻到头尾时的提示。
func TestBrowsePagingAndBoundaries(t *testing.T) {
	path := configSandbox(t, browseManyConfig(browseCount))

	// 12 条两页时的菜单是：1 新建、2 查看、3 编辑、4 删除、5 上一页、6 下一页、
	// 7 按名称筛选、8 退出。依次是：首页点上一页、翻到第二页、末页再点下一页、翻回首页、退出。
	out := browseSessionRun(t, path, runChannelsManage, "5", "6", "6", "5", "8")

	first := round(t, out, 0)
	if want := "第 1 页 / 共 2 页 · 共 12 条"; !strings.Contains(first, want) {
		t.Errorf("首页说明应当是 %q:\n%s", want, first)
	}
	if !strings.Contains(first, "svc-08") || strings.Contains(first, "alpha-01") {
		t.Errorf("首页应当是前 8 条 svc:\n%s", first)
	}

	// 在首页点上一页：页码不动，并且要说清为什么不动。
	if !strings.Contains(round(t, out, 1), "已经是第一页（第 1 页 / 共 2 页）") {
		t.Errorf("在首页点上一页应当有明确提示:\n%s", round(t, out, 1))
	}

	second := round(t, out, 2)
	if want := "第 2 页 / 共 2 页 · 共 12 条"; !strings.Contains(second, want) {
		t.Errorf("第二页说明应当是 %q:\n%s", want, second)
	}
	if !strings.Contains(second, "alpha-04") || strings.Contains(second, "svc-") {
		t.Errorf("第二页应当是最后 4 条 alpha:\n%s", second)
	}

	// 在末页点下一页：同样要说明已经到了头。
	if !strings.Contains(round(t, out, 3), "已经是最后一页（第 2 页 / 共 2 页）") {
		t.Errorf("在末页点下一页应当有明确提示:\n%s", round(t, out, 3))
	}

	// 再点上一页就回到首页，表也跟着回来。
	if !strings.Contains(round(t, out, 4), "第 1 页 / 共 2 页 · 共 12 条") {
		t.Errorf("从末页翻回上一页应当回到首页:\n%s", round(t, out, 4))
	}
}

// TestBrowseFilterNarrowsAndClearRestores 验证筛选只留匹配的条目，且能清回全量。
func TestBrowseFilterNarrowsAndClearRestores(t *testing.T) {
	path := configSandbox(t, browseManyConfig(browseCount))

	// 7 是按名称筛选；筛完只剩一页，菜单里出现清除筛选；清除后菜单恢复原样，8 是退出。
	out := browseSessionRun(t, path, runChannelsManage, "7", "alpha", "5", "8")

	filtered := round(t, out, 1)
	if want := `第 1 页 / 共 1 页 · 共 4 条 · 筛选：名字含 "alpha"`; !strings.Contains(filtered, want) {
		t.Errorf("筛选后的表头应当是 %q:\n%s", want, filtered)
	}
	for i := 1; i <= browseCount-browsePageSize; i++ {
		if name := fmt.Sprintf("alpha-%02d", i); !strings.Contains(filtered, name) {
			t.Errorf("筛选结果里缺少 %s:\n%s", name, filtered)
		}
	}
	if strings.Contains(filtered, "svc-") {
		t.Errorf("筛选结果里不该有不匹配的条目:\n%s", filtered)
	}

	// 筛选生效的那一轮菜单里要给出退回去的入口；只剩一页，也不该再摆翻页。
	menu := round(t, out, 1)
	if !strings.Contains(menu, "清除筛选（当前 alpha）") {
		t.Errorf("筛选生效后应当给出清除筛选的入口:\n%s", menu)
	}
	if strings.Contains(menu, "上一页") || strings.Contains(menu, "下一页") {
		t.Errorf("只剩一页时不该给翻页项:\n%s", menu)
	}

	// 清掉筛选之后恢复全量：表头不带筛选说明，条目又是 12 条，翻页也回来了。
	restored := round(t, out, 2)
	if !strings.Contains(restored, "第 1 页 / 共 2 页 · 共 12 条") || strings.Contains(restored, "筛选：") {
		t.Errorf("清除筛选后应当回到全量:\n%s", restored)
	}
	if !strings.Contains(restored, "svc-01") {
		t.Errorf("清除筛选后应当重新看到全量条目:\n%s", restored)
	}
	if !strings.Contains(restored, "下一页") {
		t.Errorf("清除筛选后应当又有翻页项:\n%s", restored)
	}
}

// TestBrowseFilterWithNoMatchOffersReset 验证筛不到条目时不走进死路。
func TestBrowseFilterWithNoMatchOffersReset(t *testing.T) {
	path := configSandbox(t, browseManyConfig(browseCount))

	// 筛不到时菜单只剩三项：1 新建、2 清除筛选、3 退出；这里选退出。
	out := browseSessionRun(t, path, runChannelsManage, "7", "nothing", "3")

	menu := round(t, out, 1)
	if want := `没有名字含 "nothing" 的条目，用「清除筛选」回到全量`; !strings.Contains(menu, want) {
		t.Errorf("筛不到条目时应当说明怎么退回去:\n%s", menu)
	}
	if !strings.Contains(menu, "清除筛选（当前 nothing）") {
		t.Errorf("筛不到条目时也该有清除筛选的入口:\n%s", menu)
	}
	if strings.Contains(menu, "查看") || strings.Contains(menu, "编辑") {
		t.Errorf("没有条目可挑时不该给查看与编辑:\n%s", menu)
	}
}

// detailValue 从详情里取某个标签的值，取不到时返回假值。
//
// 标签列是补齐过宽度的，值与标签之间的空格数取决于最长的那条标签，所以按行切开
// 只比值，不按空格数硬写。
func detailValue(text, label string) (string, bool) {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, label+":"); ok {
			return strings.TrimSpace(rest), true
		}
	}
	return "", false
}

// checkDetail 逐项比对详情字段。
func checkDetail(t *testing.T, detail string, want map[string]string) {
	t.Helper()
	for label, value := range want {
		got, ok := detailValue(detail, label)
		if !ok {
			t.Errorf("详情里缺少 %q 这一项:\n%s", label, detail)
			continue
		}
		if got != value {
			t.Errorf("详情的 %s 应当是 %q，实际是 %q", label, value, got)
		}
	}
}

// TestBrowseDetailShowsNestedAuth 验证挑一条能看全字段，含嵌套的 auth。
func TestBrowseDetailShowsNestedAuth(t *testing.T) {
	path := configSandbox(t, browseConfig)

	// 2 是查看，1 是下拉里第一条 primary，6 是退出。
	out := browseSessionRun(t, path, runChannelsManage, "2", "1", "6")

	detail := detailBlock(t, out, "当前渠道")
	// 鉴权的四个子字段要逐个列出来，而不是揉成「bearer ← PRIMARY_KEY」那一行缩写。
	checkDetail(t, detail, map[string]string{
		"渠道":     "primary",
		"方言":     "openai",
		"基址":     "https://api.example.com/v1",
		"路径":     "-",
		"鉴权方式":   "bearer",
		"凭据环境变量": "PRIMARY_KEY",
		"引用账号":   "-",
		"请求头名":   "-",
		"查询参数名":  "-",
		"凭据状态":   "未设置",
		"超时":     "2m0s",
		"最大重试":   "0",
		"请求头":    "-",
	})
}

// TestBrowseDetailShowsRuleFields 验证规则的详情字段。
func TestBrowseDetailShowsRuleFields(t *testing.T) {
	path := configSandbox(t, browseConfig)

	// 规则两条、一页：1 新建、2 查看、3 编辑、4 删除、5 按名称筛选、6 退出。
	out := browseSessionRun(t, path, runModelsManage, "2", "1", "6")

	detail := detailBlock(t, out, "当前规则")
	// 规则没写方言时按渠道的方言生效，详情里要写出来是哪一个。
	checkDetail(t, detail, map[string]string{
		"匹配":   "gpt-*",
		"渠道":   "primary",
		"上游模型": "同请求的模型名",
		"方言":   "openai（同渠道）",
		"亲和":   "-",
	})
}

// TestBrowseDetailShowsAccountFields 验证账号组的详情也走同一条浏览路径，并且展开成字段。
func TestBrowseDetailShowsAccountFields(t *testing.T) {
	path := configSandbox(t, accountsConfig)

	// 账号两条、一页：1 新建、2 查看、3 编辑、4 删除、5 按名称筛选、6 退出。
	out := browseSessionRun(t, path, runAccountsManage, "2", "1", "6")

	detail := detailBlock(t, out, "当前账号")
	checkDetail(t, detail, map[string]string{
		"账号":      "team-a",
		"key_env": "TEAM_A_API_KEY",
		"凭据":      "未设置",
		"引用渠道":    "pool",
		"请求头":     "-",
	})
}

// TestBrowseFilterMatchesModels 验证筛选在规则列表上走的是同一条路径。
func TestBrowseFilterMatchesModels(t *testing.T) {
	path := configSandbox(t, browseConfig)

	// 5 是按名称筛选；筛完只剩一条，6 是退出。
	out := browseSessionRun(t, path, runModelsManage, "5", "claude", "6")

	filtered := round(t, out, 1)
	if want := `第 1 页 / 共 1 页 · 共 1 条 · 筛选：名字含 "claude"`; !strings.Contains(filtered, want) {
		t.Errorf("筛选后的表头应当是 %q:\n%s", want, filtered)
	}
	if !strings.Contains(filtered, "claude-*") || strings.Contains(filtered, "gpt-*") {
		t.Errorf("筛选结果应当只剩 claude-*:\n%s", filtered)
	}
}

// TestBrowseIsReadOnly 验证翻页、筛选、看详情全程不改配置文件。
//
// 光看代码「没调 configedit」不算数：这里跑几轮真流程，按文件内容的哈希比对，
// 顺带确认配置目录里也没多出别的文件。
func TestBrowseIsReadOnly(t *testing.T) {
	path := configSandbox(t, browseManyConfig(browseCount))
	dir := filepath.Dir(path)
	before := fileDigest(t, path)
	entriesBefore := dirEntries(t, dir)

	// 每一轮都走完整的交互，覆盖翻页、筛选、清除、看详情与筛不到的情况。
	rounds := [][]string{
		{"6", "5", "8"},            // 翻到末页，再翻回首页
		{"7", "alpha", "5", "8"},   // 筛选之后清除
		{"2", "2", "6", "8"},       // 看一条详情
		{"7", "nothing", "2", "8"}, // 筛不到，再清掉
	}
	for _, lines := range rounds {
		browseSessionRun(t, path, runChannelsManage, lines...)
		if after := fileDigest(t, path); after != before {
			t.Fatalf("浏览态改了配置文件（输入 %v）:\n%s", lines, readFile(t, path))
		}
		if after := dirEntries(t, dir); after != entriesBefore {
			t.Fatalf("浏览态在配置目录里留下了文件（输入 %v）:\n%s", lines, after)
		}
	}
}

// fileDigest 是文件内容的 SHA-256，用来判断内容有没有被动过。
func fileDigest(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读配置失败: %v", err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

// dirEntries 列出目录里的文件名，用来发现浏览态留下的临时文件。
func dirEntries(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("列目录失败: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return strings.Join(names, "\n")
}

// TestBrowseSessionClampsPage 验证页码与关键字越界时夹回合法范围。
func TestBrowseSessionClampsPage(t *testing.T) {
	session := browseSession{keyword: "alpha", page: 3}

	// 页码越过筛选结果的页数时夹回末页，不是照原样打出去。
	if got := session.heading(browseCount - browsePageSize); got != `第 1 页 / 共 1 页 · 共 4 条 · 筛选：名字含 "alpha"` {
		t.Errorf("页码越界时应当夹回末页: %q", got)
	}

	// 第三条记录被删掉之后再回到菜单，页码夹回新的末页。
	if start, end := session.pageRange(browsePageSize + 1); start != browsePageSize || end != browsePageSize+1 {
		t.Errorf("页码越界时的区间不对: %d, %d", start, end)
	}

	// 空列表算 0 页，页码显示 0 而不是 1；区间是空的，不会越界取值。
	if got := session.heading(0); got != `第 0 页 / 共 0 页 · 共 0 条 · 筛选：名字含 "alpha"` {
		t.Errorf("空列表的说明不对: %q", got)
	}
	if start, end := session.pageRange(0); start != 0 || end != 0 {
		t.Errorf("空列表的区间应当是空的: %d, %d", start, end)
	}
}

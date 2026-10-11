package cmd

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"charm.land/huh/v2"
	"github.com/spf13/cobra"

	"github.com/tokenmp/core/channel"
	"github.com/tokenmp/core/config"
	"github.com/tokenmp/core/dialect"

	"github.com/tokenmp/cli/internal/configedit"
	"github.com/tokenmp/cli/internal/configmgr"
)

// channelDialectChoice 是交互里的一种方言：label 是显示名，value 是写进配置的取值。
type channelDialectChoice struct {
	label string
	value string
}

// channelDialectChoices 是交互里可选的方言，顺序是 openai、claude、responses、gemini。
var channelDialectChoices = []channelDialectChoice{
	{"openai", string(dialect.OpenAIChat)},
	{"claude", string(dialect.ClaudeMessages)},
	{"responses", string(dialect.OpenAIResponses)},
	{"gemini", string(dialect.GeminiGenerate)},
}

// channelForm 是交互流程收集到的渠道字段，保留用户输入的原文，组装时才解析。
type channelForm struct {
	Name       string
	Dialect    string
	BaseURL    string
	Path       string
	AuthStyle  string
	KeyEnv     string
	Header     string
	Query      string
	Timeout    string
	MaxRetries string
}

// channelBrowseList 组装渠道的分步管理入口。
func channelBrowseList() manageList {
	return manageList{
		title: "渠道",
		items: channelBrowseItems,
		remove: func(env *configmgr.Env, index int) (string, error) {
			return removeChannelAt(env, index)
		},
	}
}

// runChannelsManage 执行 channels 不带子命令时的分步管理流程。
//
// 每一轮先打印与 channels list 相同的表格，再问做什么；做完一件回到菜单，
// 直到用户选退出或在菜单上按 Esc 与 Ctrl-C。字段表单里中断只取消当前这件，
// 回到菜单，不落盘。退出时文件不动。
func runChannelsManage(cmd *cobra.Command, configPath string) error {
	list := channelBrowseList()
	for {
		env, err := configmgr.Load(configPath)
		if err != nil {
			return err
		}
		if err := printChannelsList(cmd, env.Config); err != nil {
			return err
		}

		action, ok, err := askManageAction(cmd, len(env.Config.Channels) > 0)
		if err != nil {
			return err
		}
		if !ok || action == manageQuit {
			return nil
		}

		var actionErr error
		switch action {
		case manageCreate:
			actionErr = createChannelInteractive(cmd, env)
		case manageEdit, manageDelete:
			index, selected, err := askManageIndex(cmd, list, env)
			if err != nil {
				return err
			}
			if !selected {
				continue
			}
			if err := printManageDetail(cmd, list, env, index); err != nil {
				return err
			}
			if action == manageEdit {
				actionErr = editChannelInteractive(cmd, env, index)
			} else {
				actionErr = deleteChannelInteractive(cmd, env, index)
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

// createChannelInteractive 走完分步表单并追加一个渠道。
func createChannelInteractive(cmd *cobra.Command, env *configmgr.Env) error {
	form, ok, err := askChannelForm(cmd, newChannelForm())
	if err != nil {
		return err
	}
	if !ok {
		return errManageCancelled
	}

	record, err := channelRecordFromForm(form)
	if err != nil {
		return err
	}
	if _, exists := env.Config.ChannelByName(record.Name); exists {
		return fmt.Errorf("渠道 %s 已存在，不能覆盖", record.Name)
	}
	entry := channelEntryOf(record)

	confirmed, err := confirmChannelWrite(cmd, env.ConfigPath, "将要追加的渠道", entry)
	if err != nil {
		return err
	}
	if !confirmed {
		return errManageCancelled
	}
	if err := applyChannelCreate(env, entry); err != nil {
		return err
	}

	fmt.Fprintf(out(cmd), "%s %s %s\n",
		palette.program.Render(record.Name),
		palette.ok.Render("已追加"),
		palette.label.Render("("+env.ConfigPath+")"),
	)
	return nil
}

// editChannelInteractive 走完分步表单并改写一个渠道，只写变化的字段。
func editChannelInteractive(cmd *cobra.Command, env *configmgr.Env, index int) error {
	current := env.Config.Channels[index]
	form, ok, err := askChannelForm(cmd, channelFormFromRecord(current))
	if err != nil {
		return err
	}
	if !ok {
		return errManageCancelled
	}

	record, err := channelRecordFromForm(form)
	if err != nil {
		return err
	}
	if record.Name != current.Name {
		if err := checkChannelRename(env.Config, current.Name, record.Name); err != nil {
			return err
		}
	}
	entry := channelEntryOf(record)
	// 请求头与账号引用不在这套分步流程的管理范围内，预览里补上现有的值，与实际结果一致。
	entry.Headers = current.Headers
	entry.Auth.Accounts = current.Auth.Accounts

	if len(channelEditFields(current, entry)) == 0 {
		fmt.Fprintln(out(cmd), palette.label.Render("没有改动"))
		return nil
	}

	confirmed, err := confirmChannelWrite(cmd, env.ConfigPath, fmt.Sprintf("将要保存的渠道（channels.%d）", index), entry)
	if err != nil {
		return err
	}
	if !confirmed {
		return errManageCancelled
	}
	if err := applyChannelEdit(env, index, entry); err != nil {
		return err
	}

	fmt.Fprintf(out(cmd), "%s %s %s\n",
		palette.program.Render(record.Name),
		palette.ok.Render(fmt.Sprintf("已保存（channels.%d）", index)),
		palette.label.Render("("+env.ConfigPath+")"),
	)
	return nil
}

// deleteChannelInteractive 确认后删除一个渠道，仍被规则引用时拒绝。
func deleteChannelInteractive(cmd *cobra.Command, env *configmgr.Env, index int) error {
	name := env.Config.Channels[index].Name
	confirmed, err := confirmManage(cmd, fmt.Sprintf("删除渠道 %s？", name))
	if err != nil {
		return err
	}
	if !confirmed {
		return errManageCancelled
	}

	removed, err := removeChannelAt(env, index)
	if err != nil {
		return err
	}
	fmt.Fprintf(out(cmd), "%s %s %s\n",
		palette.program.Render(removed),
		palette.ok.Render(fmt.Sprintf("已删除（channels.%d）", index)),
		palette.label.Render("("+env.ConfigPath+")"),
	)
	return nil
}

// confirmChannelWrite 打印将要写入的渠道条目，再问一次是否落盘。
func confirmChannelWrite(cmd *cobra.Command, path, title string, entry channelEntry) (bool, error) {
	if err := printChannelEntry(cmd, path, title, entry); err != nil {
		return false, err
	}
	return confirmManage(cmd, "写入这份配置？")
}

// applyChannelCreate 追加一个渠道条目，校验与回滚与 channels create 共用一套。
func applyChannelCreate(env *configmgr.Env, entry channelEntry) error {
	return configedit.AppendMapping(env.ConfigPath, []string{"channels"}, entry, reloadableCheck(env.ConfigPath))
}

// applyChannelEdit 按下标改写渠道，只写与当前值不同的字段。
//
// 一次写入改完整条目，中间状态不落盘；写回后重新加载校验，失败还原原文件。
func applyChannelEdit(env *configmgr.Env, index int, entry channelEntry) error {
	if index < 0 || index >= len(env.Config.Channels) {
		return fmt.Errorf("渠道下标 %d 越界，共 %d 条", index, len(env.Config.Channels))
	}
	fields := channelEditFields(env.Config.Channels[index], entry)
	if len(fields) == 0 {
		return nil
	}
	return configedit.SetSequenceFields(env.ConfigPath, []string{"channels"}, index, fields, reloadableCheck(env.ConfigPath))
}

// channelEditFields 比出编辑时要改的字段，只写变化的部分。
//
// 可选字段被清空时用 nil 删除，保持「空的可选字段不写出去」的形状；值没变的字段跳过。
func channelEditFields(current config.Channel, entry channelEntry) []configedit.SequenceField {
	before := channelEntryOf(current)
	beforeStyle := before.Auth.Style
	if beforeStyle == "" {
		beforeStyle = channel.StyleNone
	}

	var fields []configedit.SequenceField
	add := func(path []string, old, next string) {
		if old == next {
			return
		}
		if next == "" {
			fields = append(fields, configedit.SequenceField{Path: path, Value: nil})
			return
		}
		fields = append(fields, configedit.SequenceField{Path: path, Value: next})
	}

	add([]string{"name"}, before.Name, entry.Name)
	add([]string{"dialect"}, before.Dialect, entry.Dialect)
	add([]string{"base_url"}, before.BaseURL, entry.BaseURL)
	add([]string{"path"}, before.Path, entry.Path)
	add([]string{"auth", "style"}, beforeStyle, entry.Auth.Style)
	add([]string{"auth", "key_env"}, before.Auth.KeyEnv, entry.Auth.KeyEnv)
	add([]string{"auth", "header"}, before.Auth.Header, entry.Auth.Header)
	add([]string{"auth", "query"}, before.Auth.Query, entry.Auth.Query)

	if before.Timeout != entry.Timeout {
		fields = append(fields, configedit.SequenceField{Path: []string{"timeout"}, Value: entry.Timeout})
	}

	beforeRetries := retriesOf(current)
	afterRetries := 0
	if entry.MaxRetries != nil {
		afterRetries = *entry.MaxRetries
	}
	if beforeRetries != afterRetries {
		value := any(afterRetries)
		if afterRetries == 0 {
			value = nil
		}
		fields = append(fields, configedit.SequenceField{Path: []string{"max_retries"}, Value: value})
	}
	return fields
}

// checkChannelRename 报告渠道能否改名：仍被模型规则引用时不允许改名，避免规则悬空。
func checkChannelRename(cfg *config.Config, oldName, newName string) error {
	refs := referencingRules(cfg, oldName)
	if len(refs) == 0 {
		return nil
	}
	return fmt.Errorf("渠道 %s 仍被 %d 条模型规则引用（%s），改名会让它们悬空；先改这些规则或换个名字",
		oldName, len(refs), joinList(refs))
}

// askChannelForm 分步问渠道字段，回车保留默认值。
//
// 新建时用 newChannelForm 的默认值，编辑时默认值是当前值。返回 false 表示用户中断。
func askChannelForm(cmd *cobra.Command, form channelForm) (channelForm, bool, error) {
	dialectOptions := make([]huh.Option[string], 0, len(channelDialectChoices))
	for _, choice := range channelDialectChoices {
		dialectOptions = append(dialectOptions, huh.NewOption(choice.label, choice.value))
	}
	authOptions := make([]huh.Option[string], 0, len(channelAuthStyles))
	for _, style := range channelAuthStyles {
		authOptions = append(authOptions, huh.NewOption(style, style))
	}

	groups := []*huh.Group{
		huh.NewGroup(huh.NewInput().
			Title("渠道名").
			Value(&form.Name).
			Validate(validateChannelName)),
		huh.NewGroup(huh.NewSelect[string]().
			Title("方言").
			Options(dialectOptions...).
			Value(&form.Dialect)),
		huh.NewGroup(huh.NewInput().
			Title("基址").
			Value(&form.BaseURL).
			Validate(validateChannelBaseURL(&form))),
		huh.NewGroup(huh.NewInput().
			Title("路径（留空按方言取默认）").
			Value(&form.Path).
			Validate(validateChannelPath(&form))),
		huh.NewGroup(huh.NewSelect[string]().
			Title("鉴权方式").
			Options(authOptions...).
			Value(&form.AuthStyle)),
		huh.NewGroup(huh.NewInput().
			Title("凭据所在的环境变量名").
			Value(&form.KeyEnv).
			Validate(validateChannelKeyEnv(&form))).
			WithHideFunc(func() bool { return form.AuthStyle == channel.StyleNone }),
		huh.NewGroup(huh.NewInput().
			Title("自定义请求头名").
			Value(&form.Header).
			Validate(validateChannelHeader(&form))).
			WithHideFunc(func() bool { return form.AuthStyle != channel.StyleCustom }),
		huh.NewGroup(huh.NewInput().
			Title("查询参数名").
			Value(&form.Query).
			Validate(validateChannelQuery(&form))).
			WithHideFunc(func() bool { return form.AuthStyle != channel.StyleQuery }),
		huh.NewGroup(huh.NewInput().
			Title("超时（留空用默认）").
			Value(&form.Timeout).
			Validate(validateChannelTimeout)),
		huh.NewGroup(huh.NewInput().
			Title("最大重试次数（留空用 0）").
			Value(&form.MaxRetries).
			Validate(validateChannelRetries)),
	}

	ok, err := runManageForm(cmd, huh.NewForm(groups...))
	return form, ok, err
}

// newChannelForm 是新建渠道时的默认值。
func newChannelForm() channelForm {
	return channelForm{
		Dialect:   string(dialect.OpenAIChat),
		AuthStyle: channel.StyleNone,
	}
}

// channelFormFromRecord 用当前渠道填出编辑时的默认值，回车即保留。
func channelFormFromRecord(record config.Channel) channelForm {
	form := channelForm{
		Name:      record.Name,
		Dialect:   record.Dialect,
		BaseURL:   record.BaseURL,
		Path:      record.Path,
		AuthStyle: record.Auth.Style,
		KeyEnv:    record.Auth.KeyEnv,
		Header:    record.Auth.Header,
		Query:     record.Auth.Query,
		Timeout:   record.Timeout.String(),
	}
	if form.AuthStyle == "" {
		form.AuthStyle = channel.StyleNone
	}
	if record.MaxRetries != nil {
		form.MaxRetries = strconv.Itoa(*record.MaxRetries)
	}
	return form
}

// channelRecordFromForm 把表单装成 core 的渠道记录，并做与 channels create 相同的渠道校验。
func channelRecordFromForm(form channelForm) (config.Channel, error) {
	spec, err := channelSpecFromForm(form)
	if err != nil {
		return config.Channel{}, err
	}
	if err := spec.Validate(); err != nil {
		return config.Channel{}, err
	}

	style := spec.Auth.Style
	if style == "" {
		style = channel.StyleNone
	}
	record := config.Channel{
		Name:    spec.Name,
		Dialect: string(spec.Dialect),
		BaseURL: spec.BaseURL,
		Path:    spec.Path,
		Timeout: config.Duration(spec.Timeout),
	}
	if style == channel.StyleNone {
		// 不鉴权时 key_env、header、query 都没有意义，不写进文件。
		record.Auth = config.Auth{Style: channel.StyleNone}
	} else {
		record.Auth = config.Auth{
			Style:  style,
			KeyEnv: spec.Auth.KeyEnv,
			Header: spec.Auth.Header,
			Query:  spec.Auth.Query,
		}
	}
	if spec.MaxRetries != 0 {
		retries := spec.MaxRetries
		record.MaxRetries = &retries
	}
	return record, nil
}

// channelSpecFromForm 把表单装成 core 的渠道描述，供逐步校验与写回校验共用。
func channelSpecFromForm(form channelForm) (channel.Spec, error) {
	timeout, err := parseChannelTimeout(form.Timeout)
	if err != nil {
		return channel.Spec{}, err
	}
	retries, err := parseChannelRetries(form.MaxRetries)
	if err != nil {
		return channel.Spec{}, err
	}
	return channel.Spec{
		Name:    strings.TrimSpace(form.Name),
		Dialect: dialect.Name(form.Dialect),
		BaseURL: strings.TrimSpace(form.BaseURL),
		Path:    strings.TrimSpace(form.Path),
		Auth: channel.Auth{
			Style:  form.AuthStyle,
			KeyEnv: strings.TrimSpace(form.KeyEnv),
			Header: strings.TrimSpace(form.Header),
			Query:  strings.TrimSpace(form.Query),
		},
		Timeout:    timeout,
		MaxRetries: retries,
	}, nil
}

// parseChannelTimeout 解析超时，留空时用默认值。
func parseChannelTimeout(text string) (time.Duration, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return channel.DefaultTimeout, nil
	}
	timeout, err := time.ParseDuration(text)
	if err != nil {
		return 0, fmt.Errorf("超时 %q 无法解析，写法如 30s、2m", text)
	}
	if timeout <= 0 {
		return 0, errors.New("超时须为正")
	}
	return timeout, nil
}

// parseChannelRetries 解析重试次数，留空时按 0 处理。
func parseChannelRetries(text string) (int, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0, nil
	}
	retries, err := strconv.Atoi(text)
	if err != nil {
		return 0, fmt.Errorf("重试次数 %q 不是整数", text)
	}
	if retries < 0 || retries > channel.MaxRetries {
		return 0, fmt.Errorf("重试次数须在 0 到 %d 之间", channel.MaxRetries)
	}
	return retries, nil
}

func validateChannelName(value string) error {
	if strings.TrimSpace(value) == "" {
		return errors.New("渠道名不能为空")
	}
	return nil
}

func validateChannelTimeout(value string) error {
	_, err := parseChannelTimeout(value)
	return err
}

func validateChannelRetries(value string) error {
	_, err := parseChannelRetries(value)
	return err
}

// validateChannelBaseURL 校验基址形状。基址带路径却还没给 path 的情况由下一步把关。
func validateChannelBaseURL(form *channelForm) func(string) error {
	return func(value string) error {
		spec, err := channelSpecFromForm(*form)
		if err != nil {
			return err
		}
		spec.BaseURL = strings.TrimSpace(value)
		if err := spec.Validate(); err != nil {
			if strings.Contains(err.Error(), "必须显式给出 path") {
				return nil
			}
			return err
		}
		return nil
	}
}

func validateChannelPath(form *channelForm) func(string) error {
	return func(value string) error {
		spec, err := channelSpecFromForm(*form)
		if err != nil {
			return err
		}
		spec.Path = strings.TrimSpace(value)
		return spec.Validate()
	}
}

func validateChannelKeyEnv(form *channelForm) func(string) error {
	return func(value string) error {
		if form.AuthStyle == channel.StyleNone {
			return nil
		}
		if strings.TrimSpace(value) == "" {
			return errors.New("凭据环境变量名不能为空")
		}
		return nil
	}
}

func validateChannelHeader(form *channelForm) func(string) error {
	return func(value string) error {
		if form.AuthStyle != channel.StyleCustom {
			return nil
		}
		if strings.TrimSpace(value) == "" {
			return errors.New("请求头名不能为空")
		}
		return nil
	}
}

func validateChannelQuery(form *channelForm) func(string) error {
	return func(value string) error {
		if form.AuthStyle != channel.StyleQuery {
			return nil
		}
		if strings.TrimSpace(value) == "" {
			return errors.New("查询参数名不能为空")
		}
		return nil
	}
}

// channelBrowseItems 把渠道整理成交互条目。
func channelBrowseItems(env *configmgr.Env) []manageItem {
	rows := channelRows(env.Config)
	items := make([]manageItem, 0, len(rows))
	for index, row := range rows {
		items = append(items, manageItem{
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

package cmd

import (
	"errors"
	"fmt"
	"maps"
	"strings"

	"charm.land/huh/v2"
	"github.com/spf13/cobra"

	"github.com/tokenmp/core/config"

	"github.com/tokenmp/cli/internal/configedit"
	"github.com/tokenmp/cli/internal/configmgr"
)

// accountForm 是交互流程收集到的账号字段，保留用户输入的原文，组装时才处理。
type accountForm struct {
	Name    string
	KeyEnv  string
	Headers string
}

// accountBrowseList 组装账号的分步管理入口。
func accountBrowseList() manageList {
	return manageList{
		title: "账号",
		items: accountBrowseItems,
		remove: func(env *configmgr.Env, index int) (string, error) {
			return removeAccountAt(env, index)
		},
	}
}

// runAccountsManage 执行 accounts 不带子命令时的分步管理流程。
//
// 每一轮先打印与 accounts list 相同的表格，再问做什么；做完一件回到菜单，
// 直到用户选退出或在菜单上按 Esc 与 Ctrl-C。字段表单里中断只取消当前这件，
// 回到菜单，不落盘。退出时文件不动。
func runAccountsManage(cmd *cobra.Command, configPath string) error {
	list := accountBrowseList()
	for {
		env, err := configmgr.LoadAccounts(configPath)
		if err != nil {
			return err
		}
		if err := printAccountsList(cmd, env.Config); err != nil {
			return err
		}

		action, ok, err := askManageAction(cmd, len(env.Config.Accounts) > 0)
		if err != nil {
			return err
		}
		if !ok || action == manageQuit {
			return nil
		}

		var actionErr error
		switch action {
		case manageCreate:
			actionErr = createAccountInteractive(cmd, env)
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
				actionErr = editAccountInteractive(cmd, env, index)
			} else {
				actionErr = deleteAccountInteractive(cmd, env, index)
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

// createAccountInteractive 走完分步表单并追加一个账号。
func createAccountInteractive(cmd *cobra.Command, env *configmgr.Env) error {
	form, ok, err := askAccountForm(cmd, newAccountForm())
	if err != nil {
		return err
	}
	if !ok {
		return errManageCancelled
	}

	record, err := accountRecordFromForm(form)
	if err != nil {
		return err
	}
	if _, exists := accountByName(env.Config, record.Name); exists {
		return fmt.Errorf("账号 %s 已存在，不能覆盖", record.Name)
	}
	entry := accountEntryOf(record)

	confirmed, err := confirmAccountWrite(cmd, env.ConfigPath, "将要追加的账号", entry)
	if err != nil {
		return err
	}
	if !confirmed {
		return errManageCancelled
	}
	if err := applyAccountCreate(env, entry); err != nil {
		return err
	}

	fmt.Fprintf(out(cmd), "%s %s %s\n",
		palette.program.Render(record.Name),
		palette.ok.Render("已追加"),
		palette.label.Render("("+env.ConfigPath+")"),
	)
	return nil
}

// editAccountInteractive 走完分步表单并改写一个账号，只写变化的字段。
func editAccountInteractive(cmd *cobra.Command, env *configmgr.Env, index int) error {
	current := env.Config.Accounts[index]
	form, ok, err := askAccountForm(cmd, accountFormFromRecord(current))
	if err != nil {
		return err
	}
	if !ok {
		return errManageCancelled
	}

	record, err := accountRecordFromForm(form)
	if err != nil {
		return err
	}
	if record.Name != current.Name {
		if err := checkAccountRename(env.Config, current.Name, record.Name); err != nil {
			return err
		}
	}
	entry := accountEntryOf(record)

	if len(accountEditFields(current, entry)) == 0 {
		fmt.Fprintln(out(cmd), palette.label.Render("没有改动"))
		return nil
	}

	confirmed, err := confirmAccountWrite(cmd, env.ConfigPath, fmt.Sprintf("将要保存的账号（accounts.%d）", index), entry)
	if err != nil {
		return err
	}
	if !confirmed {
		return errManageCancelled
	}
	if err := applyAccountEdit(env, index, entry); err != nil {
		return err
	}

	fmt.Fprintf(out(cmd), "%s %s %s\n",
		palette.program.Render(record.Name),
		palette.ok.Render(fmt.Sprintf("已保存（accounts.%d）", index)),
		palette.label.Render("("+env.ConfigPath+")"),
	)
	return nil
}

// deleteAccountInteractive 确认后删除一个账号，仍被渠道引用时拒绝。
func deleteAccountInteractive(cmd *cobra.Command, env *configmgr.Env, index int) error {
	name := env.Config.Accounts[index].Name
	confirmed, err := confirmManage(cmd, fmt.Sprintf("删除账号 %s？", name))
	if err != nil {
		return err
	}
	if !confirmed {
		return errManageCancelled
	}

	removed, err := removeAccountAt(env, index)
	if err != nil {
		return err
	}
	fmt.Fprintf(out(cmd), "%s %s %s\n",
		palette.program.Render(removed),
		palette.ok.Render(fmt.Sprintf("已删除（accounts.%d）", index)),
		palette.label.Render("("+env.ConfigPath+")"),
	)
	return nil
}

// confirmAccountWrite 打印将要写入的账号条目，再问一次是否落盘。
func confirmAccountWrite(cmd *cobra.Command, path, title string, entry accountEntry) (bool, error) {
	if err := printAccountEntry(cmd, path, title, entry); err != nil {
		return false, err
	}
	return confirmManage(cmd, "写入这份配置？")
}

// applyAccountCreate 追加一个账号条目，校验与回滚与 accounts create 共用一套。
func applyAccountCreate(env *configmgr.Env, entry accountEntry) error {
	return configedit.AppendMapping(env.ConfigPath, []string{"accounts"}, entry, accountsReloadableCheck(env.ConfigPath))
}

// applyAccountEdit 按下标改写账号，只写与当前值不同的字段。
//
// 一次写入改完整条目，中间状态不落盘；写回后重新加载校验，失败还原原文件。
func applyAccountEdit(env *configmgr.Env, index int, entry accountEntry) error {
	if index < 0 || index >= len(env.Config.Accounts) {
		return fmt.Errorf("账号下标 %d 越界，共 %d 条", index, len(env.Config.Accounts))
	}
	fields := accountEditFields(env.Config.Accounts[index], entry)
	if len(fields) == 0 {
		return nil
	}
	return configedit.SetSequenceFields(env.ConfigPath, []string{"accounts"}, index, fields, accountsReloadableCheck(env.ConfigPath))
}

// accountEditFields 比出编辑时要改的字段，只写变化的部分。
//
// headers 被清空时用 nil 删除，保持「没有请求头不写出去」的形状；值没变的字段跳过。
func accountEditFields(current config.Account, entry accountEntry) []configedit.SequenceField {
	before := accountEntryOf(current)

	var fields []configedit.SequenceField
	add := func(key, old, next string) {
		if old == next {
			return
		}
		if next == "" {
			fields = append(fields, configedit.SequenceField{Path: []string{key}, Value: nil})
			return
		}
		fields = append(fields, configedit.SequenceField{Path: []string{key}, Value: next})
	}
	add("name", before.Name, entry.Name)
	add("key_env", before.KeyEnv, entry.KeyEnv)

	if !maps.Equal(before.Headers, entry.Headers) {
		value := any(entry.Headers)
		if len(entry.Headers) == 0 {
			value = nil
		}
		fields = append(fields, configedit.SequenceField{Path: []string{"headers"}, Value: value})
	}
	return fields
}

// checkAccountRename 报告账号能否改名：仍被渠道引用时不允许，避免引用悬空。
func checkAccountRename(cfg *config.Config, oldName, newName string) error {
	refs := referencingChannels(cfg, oldName)
	if len(refs) == 0 {
		return nil
	}
	return fmt.Errorf("账号 %s 仍被 %d 条渠道引用（%s），改名会让它们悬空；先改这些渠道的 auth.accounts 或换个名字",
		oldName, len(refs), joinList(channelRefLabels(refs)))
}

// askAccountForm 分步问账号字段，回车保留默认值。
//
// 新建时用 newAccountForm 的默认值，编辑时默认值是当前值。返回 false 表示用户中断。
func askAccountForm(cmd *cobra.Command, form accountForm) (accountForm, bool, error) {
	groups := []*huh.Group{
		huh.NewGroup(huh.NewInput().
			Title("账号名").
			Value(&form.Name).
			Validate(validateAccountName)),
		huh.NewGroup(huh.NewInput().
			Title("凭据所在的环境变量名").
			Value(&form.KeyEnv).
			Validate(validateAccountKeyEnv)),
		huh.NewGroup(huh.NewInput().
			Title("账号级请求头（k=v，逗号分隔，可留空）").
			Value(&form.Headers).
			Validate(validateAccountHeaders)),
	}

	ok, err := runManageForm(cmd, huh.NewForm(groups...))
	return form, ok, err
}

// newAccountForm 是新建账号时的默认值。
func newAccountForm() accountForm {
	return accountForm{}
}

// accountFormFromRecord 用当前账号填出编辑时的默认值，回车即保留。
func accountFormFromRecord(account config.Account) accountForm {
	return accountForm{
		Name:    account.Name,
		KeyEnv:  account.KeyEnv,
		Headers: formatHeaderText(account.Headers),
	}
}

// accountRecordFromForm 把表单装成 core 的账号记录。
func accountRecordFromForm(form accountForm) (config.Account, error) {
	name := strings.TrimSpace(form.Name)
	if name == "" {
		return config.Account{}, errors.New("账号名不能为空")
	}
	keyEnv := strings.TrimSpace(form.KeyEnv)
	if keyEnv == "" {
		return config.Account{}, errors.New("凭据环境变量名不能为空")
	}
	headers, err := parseHeaderText(form.Headers)
	if err != nil {
		return config.Account{}, err
	}
	return config.Account{Name: name, KeyEnv: keyEnv, Headers: headers}, nil
}

// parseHeaderText 把交互文本框里的「k=v, k2=v2」解析成请求头映射。
func parseHeaderText(text string) (map[string]string, error) {
	parts := strings.Split(text, ",")
	cleaned := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		cleaned = append(cleaned, part)
	}
	return parseHeaderFlags(cleaned)
}

func validateAccountName(value string) error {
	if strings.TrimSpace(value) == "" {
		return errors.New("账号名不能为空")
	}
	return nil
}

func validateAccountKeyEnv(value string) error {
	if strings.TrimSpace(value) == "" {
		return errors.New("凭据环境变量名不能为空")
	}
	return nil
}

func validateAccountHeaders(value string) error {
	_, err := parseHeaderText(value)
	return err
}

// accountBrowseItems 把账号整理成交互条目。
func accountBrowseItems(env *configmgr.Env) []manageItem {
	rows := accountRows(env.Config)
	items := make([]manageItem, 0, len(rows))
	for index, row := range rows {
		items = append(items, manageItem{
			detail: accountDetail(row, env.Config.Accounts[index].Headers),
			label:  row.Name,
		})
	}
	return items
}

// accountDetail 是账号的五个详情字段。
func accountDetail(row accountRow, headers map[string]string) [][2]string {
	return [][2]string{
		{"账号", row.Name},
		{"key_env", row.KeyEnv},
		{"凭据", row.Credential},
		{"引用渠道", dash(strings.Join(row.Channels, "、"))},
		{"请求头", headerNames(headers)},
	}
}

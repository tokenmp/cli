package cmd

import (
	"errors"
	"fmt"
	"strings"

	"charm.land/huh/v2"
	"github.com/spf13/cobra"

	"github.com/tokenmp/core/config"
	"github.com/tokenmp/core/dialect"

	"github.com/tokenmp/cli/internal/configedit"
	"github.com/tokenmp/cli/internal/configmgr"
)

// modelDialectChoices 是交互里可选的规则方言，第一项是留空，表示用渠道方言。
var modelDialectChoices = append([]channelDialectChoice{{"留空（用渠道方言）", ""}}, channelDialectChoices...)

// modelForm 是交互流程收集到的规则字段，保留用户输入的原文，组装时才处理。
type modelForm struct {
	Match    string
	Channel  string
	Upstream string
	Dialect  string
	Affinity string
}

// modelBrowseList 组装模型规则的分步管理入口。
func modelBrowseList() manageList {
	return manageList{
		title: "规则",
		items: modelBrowseItems,
		remove: func(env *configmgr.Env, index int) (string, error) {
			_, err := removeModelAt(env, index)
			if err != nil {
				return "", err
			}
			rule := env.Config.Models[index]
			return fmt.Sprintf("models.%d（match %s → %s）", index, rule.Match, rule.Channel), nil
		},
	}
}

// runModelsManage 执行 models 不带子命令时的分步管理流程。
//
// 每一轮先打印与 models list 相同的规则表，再问做什么；做完一件回到菜单，
// 直到用户选退出或在菜单上按 Esc 与 Ctrl-C。字段表单里中断只取消当前这件，
// 回到菜单，不落盘。退出时文件不动。
func runModelsManage(cmd *cobra.Command, configPath string) error {
	list := modelBrowseList()
	for {
		env, err := configmgr.Load(configPath)
		if err != nil {
			return err
		}
		if _, err := printModelsRules(cmd, modelsOutput{Rules: ruleRows(env.Config)}); err != nil {
			return err
		}

		action, ok, err := askManageAction(cmd, len(env.Config.Models) > 0)
		if err != nil {
			return err
		}
		if !ok || action == manageQuit {
			return nil
		}

		var actionErr error
		switch action {
		case manageCreate:
			actionErr = createModelInteractive(cmd, env)
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
				actionErr = editModelInteractive(cmd, env, index)
			} else {
				actionErr = deleteModelInteractive(cmd, env, index)
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

// createModelInteractive 走完分步表单并追加一条规则。
func createModelInteractive(cmd *cobra.Command, env *configmgr.Env) error {
	if len(env.Config.Channels) == 0 {
		return errors.New("配置里没有渠道，先在 channels 里建一条再写规则")
	}

	form, ok, err := askModelForm(cmd, env.Config, newModelForm(env.Config))
	if err != nil {
		return err
	}
	if !ok {
		return errManageCancelled
	}

	record, err := modelRecordFromForm(env.Config, form)
	if err != nil {
		return err
	}
	for _, rule := range env.Config.Models {
		if rule.Match == record.Match && rule.Channel == record.Channel {
			return fmt.Errorf("规则 %s → %s 已存在，不能重复添加", record.Match, record.Channel)
		}
	}
	entry := modelEntryOf(record)

	confirmed, err := confirmModelWrite(cmd, env.ConfigPath, "将要追加的规则", entry)
	if err != nil {
		return err
	}
	if !confirmed {
		return errManageCancelled
	}
	if err := applyModelCreate(env, entry); err != nil {
		return err
	}

	fmt.Fprintf(out(cmd), "%s → %s %s %s\n",
		palette.program.Render(record.Match),
		palette.program.Render(record.Channel),
		palette.ok.Render("已追加"),
		palette.label.Render("("+env.ConfigPath+")"),
	)
	return nil
}

// editModelInteractive 走完分步表单并改写一条规则，只写变化的字段。
func editModelInteractive(cmd *cobra.Command, env *configmgr.Env, index int) error {
	current := env.Config.Models[index]
	form, ok, err := askModelForm(cmd, env.Config, modelFormFromRule(current))
	if err != nil {
		return err
	}
	if !ok {
		return errManageCancelled
	}

	record, err := modelRecordFromForm(env.Config, form)
	if err != nil {
		return err
	}
	for i, rule := range env.Config.Models {
		if i != index && rule.Match == record.Match && rule.Channel == record.Channel {
			return fmt.Errorf("规则 %s → %s 已存在，不能改成它", record.Match, record.Channel)
		}
	}
	entry := modelEntryOf(record)

	if len(modelEditFields(current, entry)) == 0 {
		fmt.Fprintln(out(cmd), palette.label.Render("没有改动"))
		return nil
	}

	confirmed, err := confirmModelWrite(cmd, env.ConfigPath, fmt.Sprintf("将要保存的规则（models.%d）", index), entry)
	if err != nil {
		return err
	}
	if !confirmed {
		return errManageCancelled
	}
	if err := applyModelEdit(env, index, entry); err != nil {
		return err
	}

	fmt.Fprintf(out(cmd), "%s → %s %s %s\n",
		palette.program.Render(record.Match),
		palette.program.Render(record.Channel),
		palette.ok.Render(fmt.Sprintf("已保存（models.%d）", index)),
		palette.label.Render("("+env.ConfigPath+")"),
	)
	return nil
}

// deleteModelInteractive 确认后删除一条规则。
func deleteModelInteractive(cmd *cobra.Command, env *configmgr.Env, index int) error {
	rule := env.Config.Models[index]
	confirmed, err := confirmManage(cmd, fmt.Sprintf("删除规则 models.%d（match %s → %s）？", index, rule.Match, rule.Channel))
	if err != nil {
		return err
	}
	if !confirmed {
		return errManageCancelled
	}

	removed, err := removeModelAt(env, index)
	if err != nil {
		return err
	}
	fmt.Fprintf(out(cmd), "%s → %s %s %s\n",
		palette.program.Render(removed.Match),
		palette.program.Render(removed.Channel),
		palette.ok.Render(fmt.Sprintf("已删除（models.%d）", index)),
		palette.label.Render("("+env.ConfigPath+")"),
	)
	return nil
}

// confirmModelWrite 打印将要写入的规则条目，再问一次是否落盘。
func confirmModelWrite(cmd *cobra.Command, path, title string, entry modelEntry) (bool, error) {
	if err := printModelEntry(cmd, path, title, entry); err != nil {
		return false, err
	}
	return confirmManage(cmd, "写入这份配置？")
}

// applyModelCreate 追加一条规则，校验与回滚与 models create 共用一套。
func applyModelCreate(env *configmgr.Env, entry modelEntry) error {
	return configedit.AppendMapping(env.ConfigPath, []string{"models"}, entry, reloadableCheck(env.ConfigPath))
}

// applyModelEdit 按下标改写规则，只写与当前值不同的字段。
func applyModelEdit(env *configmgr.Env, index int, entry modelEntry) error {
	if index < 0 || index >= len(env.Config.Models) {
		return fmt.Errorf("规则下标 %d 越界，共 %d 条", index, len(env.Config.Models))
	}
	fields := modelEditFields(env.Config.Models[index], entry)
	if len(fields) == 0 {
		return nil
	}
	return configedit.SetSequenceFields(env.ConfigPath, []string{"models"}, index, fields, reloadableCheck(env.ConfigPath))
}

// modelEditFields 比出编辑时要改的字段，只写变化的部分。
//
// 可选字段被清空时用 nil 删除，保持空值不写出去；值没变的字段跳过。
func modelEditFields(current config.Model, entry modelEntry) []configedit.SequenceField {
	before := modelEntryOf(current)
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
	add("match", before.Match, entry.Match)
	add("channel", before.Channel, entry.Channel)
	add("upstream", before.Upstream, entry.Upstream)
	add("dialect", before.Dialect, entry.Dialect)
	add("affinity", before.Affinity, entry.Affinity)
	return fields
}

// askModelForm 分步问规则字段，回车保留默认值。
//
// 新建时用 newModelForm 的默认值，编辑时默认值是当前值。返回 false 表示用户中断。
func askModelForm(cmd *cobra.Command, cfg *config.Config, form modelForm) (modelForm, bool, error) {
	channelOptions := make([]huh.Option[string], 0, len(cfg.Channels))
	for _, record := range cfg.Channels {
		channelOptions = append(channelOptions, huh.NewOption(fmt.Sprintf("%s（%s）", record.Name, record.Dialect), record.Name))
	}
	dialectOptions := make([]huh.Option[string], 0, len(modelDialectChoices))
	for _, choice := range modelDialectChoices {
		dialectOptions = append(dialectOptions, huh.NewOption(choice.label, choice.value))
	}

	groups := []*huh.Group{
		huh.NewGroup(huh.NewInput().
			Title("匹配式（支持 * 与 ?）").
			Value(&form.Match).
			Validate(validateModelMatch)),
		huh.NewGroup(huh.NewSelect[string]().
			Title("渠道").
			Options(channelOptions...).
			Value(&form.Channel)),
		huh.NewGroup(huh.NewInput().
			Title("上游模型名（可写 ${model}）").
			Value(&form.Upstream).
			Validate(validateModelUpstream)),
		huh.NewGroup(huh.NewSelect[string]().
			Title("方言（留空用渠道方言）").
			Options(dialectOptions...).
			Value(&form.Dialect)),
		huh.NewGroup(huh.NewInput().
			Title("亲和组名（留空表示不参与）").
			Value(&form.Affinity)),
	}

	ok, err := runManageForm(cmd, huh.NewForm(groups...))
	return form, ok, err
}

// newModelForm 是新建规则时的默认值：渠道取配置里的第一条，方言留空。
func newModelForm(cfg *config.Config) modelForm {
	form := modelForm{}
	if len(cfg.Channels) > 0 {
		form.Channel = cfg.Channels[0].Name
	}
	return form
}

// modelFormFromRule 用当前规则填出编辑时的默认值，回车即保留。
func modelFormFromRule(rule config.Model) modelForm {
	return modelForm{
		Match:    rule.Match,
		Channel:  rule.Channel,
		Upstream: rule.Upstream,
		Dialect:  rule.Dialect,
		Affinity: rule.Affinity,
	}
}

// modelRecordFromForm 把表单装成 core 的规则，并校验匹配式、渠道与占位符。
func modelRecordFromForm(cfg *config.Config, form modelForm) (config.Model, error) {
	match := strings.TrimSpace(form.Match)
	if match == "" {
		return config.Model{}, errors.New("匹配式不能为空")
	}
	if _, err := config.CompileMatch(match); err != nil {
		return config.Model{}, err
	}

	channelName := strings.TrimSpace(form.Channel)
	if channelName == "" {
		return config.Model{}, errors.New("必须选择一个渠道")
	}
	if _, ok := cfg.ChannelByName(channelName); !ok {
		return config.Model{}, fmt.Errorf("渠道 %s 不存在", channelName)
	}

	dialectName := strings.TrimSpace(form.Dialect)
	if dialectName != "" {
		if _, ok := dialect.Parse(dialectName); !ok {
			return config.Model{}, fmt.Errorf("方言 %q 不是支持的取值", dialectName)
		}
	}

	upstream := strings.TrimSpace(form.Upstream)
	if err := validateModelUpstream(upstream); err != nil {
		return config.Model{}, err
	}

	return config.Model{
		Match:    match,
		Channel:  channelName,
		Upstream: upstream,
		Dialect:  dialectName,
		Affinity: strings.TrimSpace(form.Affinity),
	}, nil
}

func validateModelMatch(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return errors.New("匹配式不能为空")
	}
	_, err := config.CompileMatch(value)
	return err
}

// validateModelUpstream 校验占位符：只认 ${model}，且 ${ 必须有配对的 }。
func validateModelUpstream(value string) error {
	for i := 0; i < len(value); i++ {
		if value[i] != '$' || i+1 >= len(value) || value[i+1] != '{' {
			continue
		}
		rest := value[i+2:]
		end := strings.IndexByte(rest, '}')
		if end < 0 {
			return errors.New("上游模型名里的 ${ 没有配对的 }")
		}
		if name := rest[:end]; name != "model" {
			return fmt.Errorf("上游模型名里的 ${%s} 不是支持的占位符，只支持 ${model}", name)
		}
		i += len("${") + end
	}
	return nil
}

// modelBrowseItems 把规则整理成交互条目。
func modelBrowseItems(env *configmgr.Env) []manageItem {
	rows := ruleRows(env.Config)
	items := make([]manageItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, manageItem{
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

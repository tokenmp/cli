package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tokenmp/core/channel"
	"github.com/tokenmp/core/config"
	"github.com/tokenmp/core/dialect"

	"github.com/tokenmp/cli/internal/configedit"
	"github.com/tokenmp/cli/internal/configmgr"
)

// importSourceNewAPI 是 --from 目前唯一支持的取值。
const importSourceNewAPI = "new-api"

// importGeminiAuthHeader 是 Gemini 上游认的凭据请求头，与 new-api 保持一致。
const importGeminiAuthHeader = "x-goog-api-key"

// importVertexAIType 是 new-api 的 Vertex AI 类型编号。它的上游凭据是服务账号，
// 与 Gemini 的一把 API key 不同，导入后要单独确认鉴权。
const importVertexAIType = 41

// importRuleKeySeparator 拼去重用的规则键。模型名与渠道名都不含这个字符，拼起来
// 不会撞车。
const importRuleKeySeparator = "\x00"

func newImportCmd() *cobra.Command {
	var (
		source     string
		file       string
		configPath string
		dryRun     bool
	)

	cmd := &cobra.Command{
		Use:   "import",
		Short: "从其他工具导出的文件追加渠道与模型规则",
		Long: `从其他工具导出的文件往配置里追加渠道与模型规则。

目前只支持 new-api 一个来源，输入是它渠道列表接口的响应 JSON，渠道在 data.items
数组里。本命令不连 new-api 的 API、也不读它的数据库。列表接口查询时把 key 字段
排除在外，拿不到明文密钥，因此每个渠道只写 key_env，变量值由你自己设，输出里会
列出需要设置哪些变量。

类型到方言的映射：
 - OpenAI 兼容的渠道归 openai 方言，包含 1、6、7、9、10、12、13、19、20、22、25、31、40、42、43、47、48、53
 - Anthropic（14）归 claude 方言
 - Gemini（24）与 Vertex AI（41）归 gemini 方言
 - 其余类型不在表里，报错并指出是哪一条渠道，不会静默归到 openai

base_url 带了路径时按它算出要写进配置的 path，算不出来就跳过并说明。models 字段
按逗号切分，每个模型写一条规则，channel 指向该渠道。渠道名重复时跳过并说明，不会
写出重复条目。

写回逐条走 configedit，保留注释与缩进。先把全部条目在内存里校验一遍，再先写渠道、
后写规则；中途写不进去时说明已经写进去几条。导入改的是配置文件，下一次 run 才生效。`,
		Example: `  # new-api 没有导出渠道的入口，从它的列表接口取一份 JSON
  # curl -H "Authorization: Bearer <root token>" https://new-api.example.com/api/channel/ > channels.json

  # 先看将要写什么，不落盘
  tokenmp import --from new-api --file channels.json --dry-run

  # 写进配置，再按输出提示设置环境变量
  tokenmp import --from new-api --file channels.json --config config.yaml`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if source == "" {
				return errors.New("必须给出 --from")
			}
			if source != importSourceNewAPI {
				return fmt.Errorf("数据源 %q 不支持，可选 %s", source, importSourceNewAPI)
			}
			if file == "" {
				return errors.New("必须给出 --file")
			}

			data, err := os.ReadFile(file)
			if err != nil {
				return fmt.Errorf("读渠道列表 %s 失败: %w", file, err)
			}

			env, err := configmgr.Load(configPath)
			if err != nil {
				return err
			}

			plan, err := buildImportPlan(env.Config, data)
			if err != nil {
				return err
			}

			if dryRun {
				return printImportSummary(cmd, env.ConfigPath, plan, true)
			}

			var channelsWritten, rulesWritten int
			for _, item := range plan.channels {
				if err := configedit.AppendMapping(env.ConfigPath, []string{"channels"}, item.entry, reloadableCheck(env.ConfigPath)); err != nil {
					return fmt.Errorf("写入渠道 %s 失败，已经写入 %d 个渠道、%d 条规则: %w",
						item.entry.Name, channelsWritten, rulesWritten, err)
				}
				channelsWritten++
			}
			for _, item := range plan.rules {
				if err := configedit.AppendMapping(env.ConfigPath, []string{"models"}, item.entry, reloadableCheck(env.ConfigPath)); err != nil {
					return fmt.Errorf("写入规则 %s → %s 失败，已经写入 %d 个渠道、%d 条规则: %w",
						item.entry.Match, item.entry.Channel, channelsWritten, rulesWritten, err)
				}
				rulesWritten++
			}

			return printImportSummary(cmd, env.ConfigPath, plan, false)
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&source, "from", "", "数据源，目前只支持 new-api")
	flags.StringVar(&file, "file", "", "导出的 JSON 文件路径")
	flags.StringVar(&configPath, "config", "config.yaml", "配置文件路径")
	flags.BoolVar(&dryRun, "dry-run", false, "只打印将要写入的条目，不落盘")

	return cmd
}

// newAPIChannel 是 new-api 渠道列表里的一条渠道，字段名取自 model.Channel 的
// JSON 标签。
//
// key 字段在列表接口的查询里被 Omit 掉，正常恒为空串；保留它只为在文件被手工改过
// 时发出提醒，绝不写进配置。
type newAPIChannel struct {
	ID      int    `json:"id"`
	Type    int    `json:"type"`
	Name    string `json:"name"`
	Key     string `json:"key"`
	BaseURL string `json:"base_url"`
	Models  string `json:"models"`
}

// newAPIList 是渠道列表接口的响应信封。接口返回 {"success":...,"data":{"items":[...]}}。
// 顶层 items 是为了容忍只截出了 data 段的文件。
type newAPIList struct {
	Success bool            `json:"success"`
	Message string          `json:"message"`
	Data    newAPIData      `json:"data"`
	Items   []newAPIChannel `json:"items"`
}

// newAPIData 是响应信封里的 data 段。
type newAPIData struct {
	Items []newAPIChannel `json:"items"`
}

// parseNewAPIChannels 从 JSON 里取出渠道数组，兼容完整信封、只含 items 的对象与
// 纯数组三种形状。
func parseNewAPIChannels(data []byte) ([]newAPIChannel, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, errors.New("渠道列表是空的")
	}
	if trimmed[0] == '[' {
		var channels []newAPIChannel
		if err := json.Unmarshal(trimmed, &channels); err != nil {
			return nil, fmt.Errorf("解析渠道列表失败: %w", err)
		}
		return channels, nil
	}

	var list newAPIList
	if err := json.Unmarshal(trimmed, &list); err != nil {
		return nil, fmt.Errorf("解析渠道列表失败: %w", err)
	}
	if !list.Success && list.Message != "" {
		return nil, fmt.Errorf("文件里的响应不成功: %s", list.Message)
	}
	if list.Data.Items != nil {
		return list.Data.Items, nil
	}
	if list.Items != nil {
		return list.Items, nil
	}
	return nil, errors.New("文件里没有 data.items，确认它取自带渠道列表的响应")
}

// newAPIChannelType 是 new-api constant/channel.go 里的一个渠道类型及它可以落到
// 的 tokenmp 方言。dialect 为空表示 tokenmp 没有能表达它上游协议的方言。
type newAPIChannelType struct {
	id      int
	name    string
	dialect dialect.Name
}

// newAPIChannelTypes 覆盖 new-api v0.13.2 的渠道类型编号。编号与名字逐条对上那份
// 源码，升级依赖或对不上时按 constant/channel.go 重新核对。
//
// 只有上游确实按 OpenAI Chat Completions、Claude Messages 或 Gemini generateContent
// 说话，并且路径与 tokenmp 的方言默认路径一致的类型才给出 dialect，其余留空。
// 像 Azure 的 /openai/deployments/{model}/... 与 Perplexity 的 /chat/completions
// 这类路径，光靠 base_url 加 path 拼不出来（或者要按模型名动态取路径），只能手工建。
// 标为空的类型导入时会被拒绝，不会静默归到 openai。
var newAPIChannelTypes = []newAPIChannelType{
	{0, "Unknown", ""},
	{1, "OpenAI", dialect.OpenAIChat},
	{2, "Midjourney", ""},
	{3, "Azure", ""},
	{4, "Ollama", ""},
	{5, "MidjourneyPlus", ""},
	{6, "OpenAIMax", dialect.OpenAIChat},
	{7, "OhMyGPT", dialect.OpenAIChat},
	{8, "Custom", ""},
	{9, "AILS", dialect.OpenAIChat},
	{10, "AIProxy", dialect.OpenAIChat},
	{11, "PaLM", ""},
	{12, "API2GPT", dialect.OpenAIChat},
	{13, "AIGC2D", dialect.OpenAIChat},
	{14, "Anthropic", dialect.ClaudeMessages},
	{15, "Baidu", ""},
	{16, "Zhipu", ""},
	{17, "Ali", ""},
	{18, "Xunfei", ""},
	{19, "360", dialect.OpenAIChat},
	{20, "OpenRouter", dialect.OpenAIChat},
	{21, "AIProxyLibrary", ""},
	{22, "FastGPT", dialect.OpenAIChat},
	{23, "Tencent", ""},
	{24, "Gemini", dialect.GeminiGenerate},
	{25, "Moonshot", dialect.OpenAIChat},
	{26, "ZhipuV4", ""},
	{27, "Perplexity", ""},
	{31, "LingYiWanWu", dialect.OpenAIChat},
	{33, "AWS", ""},
	{34, "Cohere", ""},
	{35, "MiniMax", ""},
	{36, "SunoAPI", ""},
	{37, "Dify", ""},
	{38, "Jina", ""},
	{39, "Cloudflare", ""},
	{40, "SiliconFlow", dialect.OpenAIChat},
	{41, "VertexAI", dialect.GeminiGenerate},
	{42, "Mistral", dialect.OpenAIChat},
	{43, "DeepSeek", dialect.OpenAIChat},
	{44, "MokaAI", ""},
	{45, "VolcEngine", ""},
	{46, "BaiduV2", ""},
	{47, "Xinference", dialect.OpenAIChat},
	{48, "xAI", dialect.OpenAIChat},
	{49, "Coze", ""},
	{50, "Kling", ""},
	{51, "Jimeng", ""},
	{52, "Vidu", ""},
	{53, "Submodel", dialect.OpenAIChat},
	{54, "DoubaoVideo", ""},
	{55, "Sora", ""},
	{56, "Replicate", ""},
	{57, "Codex", ""},
}

// newAPIChannelTypeByID 查一个类型编号。第二个返回值表示编号是否在表里。
func newAPIChannelTypeByID(id int) (newAPIChannelType, bool) {
	for _, item := range newAPIChannelTypes {
		if item.id == id {
			return item, true
		}
	}
	return newAPIChannelType{}, false
}

// importChannel 是准备写进 channels 段的一条渠道。
type importChannel struct {
	entry channelEntry
}

// importRule 是准备写进 models 段的一条规则。
type importRule struct {
	entry modelEntry
}

// importSkip 记录一条没有导入的条目与原因。
type importSkip struct {
	name   string
	reason string
}

// importPlan 是一次导入的全部产物。
type importPlan struct {
	channels []importChannel
	rules    []importRule
	skips    []importSkip
	notes    []string
}

// keyEnvs 按渠道顺序返回这次导入需要的环境变量名。
func (p *importPlan) keyEnvs() []string {
	envs := make([]string, 0, len(p.channels))
	for _, item := range p.channels {
		if item.entry.Auth.KeyEnv != "" {
			envs = append(envs, item.entry.Auth.KeyEnv)
		}
	}
	return envs
}

// buildImportPlan 把一份 new-api 渠道列表变成待写入的条目。
//
// 这一步只读不写：全部条目先在内存里过一遍校验，包括类型能不能映射、base_url 能不能
// 算出 path、渠道描述本身是否合法、规则引用的渠道在导入后是否存在、名字与规则是否
// 与配置或文件内重复。只有拿到一份完整计划之后才会动配置文件。
func buildImportPlan(cfg *config.Config, data []byte) (*importPlan, error) {
	raw, err := parseNewAPIChannels(data)
	if err != nil {
		return nil, err
	}

	plan := &importPlan{}

	configured := make(map[string]bool, len(cfg.Channels))
	envNames := make(map[string]bool, len(cfg.Channels))
	for _, item := range cfg.Channels {
		configured[item.Name] = true
		if item.Auth.KeyEnv != "" {
			envNames[item.Auth.KeyEnv] = true
		}
	}
	rules := make(map[string]bool, len(cfg.Models))
	for _, item := range cfg.Models {
		rules[item.Match+importRuleKeySeparator+item.Channel] = true
	}

	// 导入后配置里会有的渠道名，规则引用它时用它判断。
	finalChannels := make(map[string]bool, len(configured))
	for name := range configured {
		finalChannels[name] = true
	}
	seenInFile := make(map[string]bool, len(raw))

	var unsupported []string
	for _, item := range raw {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			plan.skips = append(plan.skips, importSkip{name: fmt.Sprintf("#%d", item.ID), reason: "渠道名为空"})
			continue
		}
		if seenInFile[name] {
			plan.skips = append(plan.skips, importSkip{name: name, reason: "同一份文件里重名"})
			continue
		}
		if configured[name] {
			plan.skips = append(plan.skips, importSkip{name: name, reason: "配置里已有同名渠道"})
			continue
		}

		mapped, known := newAPIChannelTypeByID(item.Type)
		if !known || mapped.dialect == "" {
			detail := fmt.Sprintf("%s（类型 %d）", name, item.Type)
			if known {
				detail = fmt.Sprintf("%s（类型 %d %s）", name, item.Type, mapped.name)
			}
			unsupported = append(unsupported, detail)
			continue
		}

		if item.Key != "" {
			plan.notes = append(plan.notes, fmt.Sprintf("渠道 %s 的 JSON 里带了 key，按口径不写进配置，只保留环境变量名", name))
		}
		if mapped.id == importVertexAIType {
			plan.notes = append(plan.notes, fmt.Sprintf("渠道 %s 是 Vertex AI，凭据是服务账号而非一把 API key，导入后要单独确认鉴权写法", name))
		}

		baseURL := strings.TrimSpace(item.BaseURL)
		if baseURL == "" {
			plan.skips = append(plan.skips, importSkip{name: name, reason: "没有 base_url，无法确定上游基址"})
			continue
		}
		path, err := importChannelPath(mapped.dialect, baseURL)
		if err != nil {
			plan.skips = append(plan.skips, importSkip{name: name, reason: err.Error()})
			continue
		}

		keyEnv, note := importKeyEnv(name, envNames)
		if note != "" {
			plan.notes = append(plan.notes, note)
		}

		entry := channelEntry{
			Name:    name,
			Dialect: string(mapped.dialect),
			BaseURL: baseURL,
			Path:    path,
			Auth:    importAuth(mapped.dialect, keyEnv),
		}
		if err := specOfEntry(entry).Validate(); err != nil {
			plan.skips = append(plan.skips, importSkip{name: name, reason: err.Error()})
			continue
		}

		seenInFile[name] = true
		finalChannels[name] = true
		plan.channels = append(plan.channels, importChannel{entry: entry})

		for _, model := range importModels(item.Models) {
			if rules[model+importRuleKeySeparator+name] {
				plan.skips = append(plan.skips, importSkip{name: model, reason: fmt.Sprintf("规则 %s → %s 已存在", model, name)})
				continue
			}
			if _, err := config.CompileMatch(model); err != nil {
				plan.skips = append(plan.skips, importSkip{name: model, reason: err.Error()})
				continue
			}
			rules[model+importRuleKeySeparator+name] = true
			plan.rules = append(plan.rules, importRule{entry: modelEntry{Match: model, Channel: name}})
		}
	}

	if len(unsupported) > 0 {
		return nil, fmt.Errorf("有 %d 条渠道的类型不在支持的映射表里：%s。这些渠道需要手工建，先确认上游协议再选方言",
			len(unsupported), joinList(unsupported))
	}

	// 规则引用的渠道必须在导入后存在。上面只在能建出渠道时才追加规则，这里再兜一遍，
	// 免得后续改动让一条规则指向空渠道。
	kept := plan.rules[:0]
	for _, item := range plan.rules {
		if finalChannels[item.entry.Channel] {
			kept = append(kept, item)
			continue
		}
		plan.skips = append(plan.skips, importSkip{
			name:   item.entry.Match,
			reason: fmt.Sprintf("规则引用的渠道 %s 不在配置里", item.entry.Channel),
		})
	}
	plan.rules = kept

	return plan, nil
}

// importModels 把 new-api 的 models 字段拆成模型名序列。字段按逗号切分，空格与空段
// 丢掉，重复的只留一条。
func importModels(raw string) []string {
	seen := make(map[string]bool)
	models := make([]string, 0)
	for _, part := range strings.Split(raw, ",") {
		model := strings.TrimSpace(part)
		if model == "" || seen[model] {
			continue
		}
		seen[model] = true
		models = append(models, model)
	}
	return models
}

// importAuth 给出导入渠道的鉴权写法。凭据只写环境变量名，值由用户自己设。
//
// Claude 与 new-api 一样用 x-api-key 头；Gemini 用 x-goog-api-key 头，tokenmp 用
// custom 样式表达；OpenAI 兼容的用 Bearer 头。
func importAuth(name dialect.Name, keyEnv string) authEntry {
	switch name {
	case dialect.OpenAIChat:
		return authEntry{Style: channel.StyleBearer, KeyEnv: keyEnv}
	case dialect.ClaudeMessages:
		return authEntry{Style: channel.StyleAPIKey, KeyEnv: keyEnv}
	case dialect.GeminiGenerate:
		return authEntry{Style: channel.StyleCustom, KeyEnv: keyEnv, Header: importGeminiAuthHeader}
	default:
		return authEntry{Style: channel.StyleNone}
	}
}

// importKeyEnv 按渠道名推导凭据环境变量名，重名时换一个并给出说明。
//
// 推导规则是把渠道名里的非字母数字折成下划线再大写，末尾接 _API_KEY；渠道名本身
// 以 _API_KEY 结尾时不再重复接。与已用名字（配置里已有的、这次文件里先出现的）
// 冲突时接 _2、_3，并把换名这件事写进小结。
func importKeyEnv(name string, used map[string]bool) (string, string) {
	base := normalizeEnvName(name)
	env := base + "_API_KEY"
	if strings.HasSuffix(base, "_API_KEY") {
		env = base
	}

	if !used[env] {
		used[env] = true
		return env, ""
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s_%d", env, i)
		if used[candidate] {
			continue
		}
		used[candidate] = true
		return candidate, fmt.Sprintf("渠道 %s 的凭据变量 %s 已被占用，改用 %s", name, env, candidate)
	}
}

// normalizeEnvName 把渠道名变成环境变量名的词干：字母数字保留并大写，其余字符折成
// 下划线，连续下划线合并，首尾去掉。名字里没有可用字符时退回 CHANNEL。
func normalizeEnvName(name string) string {
	var builder strings.Builder
	underscore := false
	for _, character := range name {
		switch {
		case character >= 'a' && character <= 'z':
			builder.WriteRune(character - 'a' + 'A')
			underscore = false
		case (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9'):
			builder.WriteRune(character)
			underscore = false
		default:
			if !underscore && builder.Len() > 0 {
				builder.WriteByte('_')
				underscore = true
			}
		}
	}
	result := strings.Trim(builder.String(), "_")
	if result == "" {
		return "CHANNEL"
	}
	return result
}

// importChannelPath 按 base_url 的路径算出要写进配置的 path。
//
// new-api 把方言的上游路径整段拼在 base_url 之后，tokenmp 用 path 表达这段后缀：
//
//	https://openrouter.ai/api   + /v1/chat/completions
//	https://api.openai.com/v1    + /chat/completions
//
// 两种写法指向同一个上游端点。因此这里取方言的默认路径，并把 base_url 路径里与默认
// 路径开头重合的段去掉：那一段已经由 base_url 自己承担。base_url 不带路径时不需要
// 写 path，返回空串。
//
// 算不出来时返回错误，由调用方跳过这条渠道并说明原因。
func importChannelPath(name dialect.Name, baseURL string) (string, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("base_url %q 无法解析", baseURL)
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("base_url %q 带了查询串或片段，无法确定上游路径", baseURL)
	}
	basePath := strings.Trim(parsed.Path, "/")
	if basePath == "" {
		return "", nil
	}
	if name == dialect.GeminiGenerate {
		return "", errors.New("gemini 的路径含模型名，base_url 带路径时无法补出 path")
	}
	defaultPath, err := name.UpstreamPath("", false)
	if err != nil {
		return "", err
	}
	path := trimImportPathOverlap(basePath, defaultPath)
	if path == "" {
		return "", fmt.Errorf("base_url %q 已经包含了上游路径 %q，拆不出要写的 path", baseURL, defaultPath)
	}
	return path, nil
}

// trimImportPathOverlap 返回默认路径去掉 basePath 后缀重合部分之后剩下的那一段。
//
// basePath 与 defaultPath 都是不含首尾斜杠的路径。重合按整段比较，basePath 的末尾
// 几段与 defaultPath 的开头几段一致时就算重合；没有重合时整段默认路径都要拼上去。
// defaultPath 被完全覆盖时返回空串，由调用方当成算不出来处理。
func trimImportPathOverlap(basePath, defaultPath string) string {
	baseSegments := strings.Split(basePath, "/")
	defaultSegments := strings.Split(strings.Trim(defaultPath, "/"), "/")

	shared := 0
	for k := 1; k <= len(baseSegments) && k <= len(defaultSegments); k++ {
		if equalSegments(baseSegments[len(baseSegments)-k:], defaultSegments[:k]) {
			shared = k
		}
	}
	if shared == 0 {
		return defaultPath
	}
	if shared == len(defaultSegments) {
		return ""
	}
	return "/" + strings.Join(defaultSegments[shared:], "/")
}

// equalSegments 报告两段路径是否逐段相同。
func equalSegments(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

// specOfEntry 把写回形状的渠道还原成 core 的渠道描述，用于写盘前的校验。
func specOfEntry(entry channelEntry) channel.Spec {
	return channel.Spec{
		Name:    entry.Name,
		Dialect: dialect.Name(entry.Dialect),
		BaseURL: entry.BaseURL,
		Path:    entry.Path,
		Auth: channel.Auth{
			Style:  entry.Auth.Style,
			KeyEnv: entry.Auth.KeyEnv,
			Header: entry.Auth.Header,
			Query:  entry.Auth.Query,
		},
	}
}

// printImportSummary 打印这次导入的渠道、规则、需要的环境变量与被跳过的条目。
func printImportSummary(cmd *cobra.Command, path string, plan *importPlan, dryRun bool) error {
	title := "已追加的渠道与规则"
	if dryRun {
		title = "将要追加的渠道与规则"
	}

	writer := out(cmd)
	fmt.Fprintf(writer, "%s %s\n\n", palette.title.Render(title), palette.label.Render("("+path+")"))

	renderImportChannels(writer, plan)
	renderImportRules(writer, plan)

	fields := [][2]string{
		{"渠道", fmt.Sprintf("%d 条", len(plan.channels))},
		{"规则", fmt.Sprintf("%d 条", len(plan.rules))},
	}
	if envs := plan.keyEnvs(); len(envs) > 0 {
		fields = append(fields, [2]string{"环境变量", joinList(envs)})
	}
	fmt.Fprintln(writer)
	printFields(writer, fields)

	if len(plan.skips) > 0 {
		fmt.Fprintf(writer, "\n%s\n", palette.warn.Render(fmt.Sprintf("跳过 %d 条：", len(plan.skips))))
		for _, item := range plan.skips {
			fmt.Fprintf(writer, " - %s：%s\n", item.name, item.reason)
		}
	}
	for _, note := range plan.notes {
		fmt.Fprintf(writer, "%s %s\n", palette.warn.Render("注意"), note)
	}
	if dryRun {
		fmt.Fprintf(writer, "\n%s\n", palette.label.Render("没有写进配置文件（--dry-run）"))
	}
	return nil
}

// renderImportChannels 打印这次导入的渠道表。
func renderImportChannels(writer io.Writer, plan *importPlan) {
	if len(plan.channels) == 0 {
		return
	}
	channels := newTable().Headers("渠道", "方言", "基址", "路径", "凭据变量")
	for _, item := range plan.channels {
		channels.Row(item.entry.Name, item.entry.Dialect, item.entry.BaseURL, dash(item.entry.Path), item.entry.Auth.KeyEnv)
	}
	fmt.Fprintln(writer, channels.Render())
}

// renderImportRules 打印这次导入的模型规则表。
func renderImportRules(writer io.Writer, plan *importPlan) {
	if len(plan.rules) == 0 {
		return
	}
	fmt.Fprintf(writer, "\n%s\n", palette.title.Render("模型规则"))
	rules := newTable().Headers("模型", "渠道")
	for _, item := range plan.rules {
		rules.Row(item.entry.Match, item.entry.Channel)
	}
	fmt.Fprintln(writer, rules.Render())
}

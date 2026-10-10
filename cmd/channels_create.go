package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/tokenmp/core/channel"
	"github.com/tokenmp/core/config"
	"github.com/tokenmp/core/dialect"

	"github.com/tokenmp/cli/internal/configedit"
	"github.com/tokenmp/cli/internal/configmgr"
)

// channelAuthStyles 是 --auth-style 的合法取值，也是报错时列出的取值。
var channelAuthStyles = []string{
	channel.StyleBearer,
	channel.StyleAPIKey,
	channel.StyleQuery,
	channel.StyleCustom,
	channel.StyleNone,
}

func newChannelsCreateCmd(opts *channelsOptions) *cobra.Command {
	var (
		name        string
		dialectName string
		baseURL     string
		path        string
		keyEnv      string
		authStyle   string
		authHeader  string
		authQuery   string
		timeout     time.Duration
		maxRetries  int
		dryRun      bool
	)

	cmd := &cobra.Command{
		Use:   "create",
		Short: "追加一个渠道",
		Long: `追加一个渠道。

渠道名重复时报错，不覆盖已有的条目。追加前先按渠道规则校验，例如 base_url 带了路径
却没给 path、鉴权方式与参数对不上，都在写文件之前拦下。

--auth-style 不写时按 --key-env 推断：给了 key-env 就是 bearer，没给就是 none。
取值是 bearer、x-api-key、query、custom 与 none。

写回保留注释与缩进，写回后重新加载校验，配置不合法就还原原文件，与 config set 同一套。
没有写在配置里的可选字段不会被写出来。`,
		Example: `  # 走 OpenAI 协议，凭据放在 Bearer 头里
  tokenmp channels create --name openai --dialect openai --base-url https://api.openai.com --key-env OPENAI_API_KEY

  # 基址自带路径时必须给出 path
  tokenmp channels create --name openrouter --dialect openai --base-url https://openrouter.ai/api/v1 --path /chat/completions --key-env OPENROUTER_API_KEY

  # 不鉴权的本地假上游，先看将要写什么
  tokenmp channels create --name local --dialect openai --base-url http://127.0.0.1:8080 --dry-run`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if name == "" {
				return errors.New("必须给出 --name")
			}
			if dialectName == "" {
				return errors.New("必须给出 --dialect")
			}
			if baseURL == "" {
				return errors.New("必须给出 --base-url")
			}
			if timeout <= 0 {
				return errors.New("超时必须为正")
			}
			if authStyle != "" && !inChannelAuthStyles(authStyle) {
				return fmt.Errorf("鉴权方式 %q 不是支持的取值，可选 %s", authStyle, joinList(channelAuthStyles))
			}

			env, err := configmgr.Load(opts.configPath)
			if err != nil {
				return err
			}
			if _, exists := env.Config.ChannelByName(name); exists {
				return fmt.Errorf("渠道 %s 已存在，不能覆盖", name)
			}

			if authStyle == "" {
				if keyEnv != "" {
					authStyle = channel.StyleBearer
				} else {
					authStyle = channel.StyleNone
				}
			}

			// 先按渠道规则校验。core 的 config.Validate 只看 base_url 非空、
			// 是不是 http 前缀，看不出「带了路径却没给 path」，那条规则只在这里。
			spec := channel.Spec{
				Name:       name,
				Dialect:    dialect.Name(dialectName),
				BaseURL:    baseURL,
				Path:       path,
				Auth:       coreAuth(authStyle, keyEnv, authHeader, authQuery),
				Timeout:    timeout,
				MaxRetries: maxRetries,
			}
			if err := spec.Validate(); err != nil {
				return err
			}

			record := config.Channel{
				Name:    name,
				Dialect: dialectName,
				BaseURL: baseURL,
				Path:    path,
				Auth:    config.Auth{Style: authStyle, KeyEnv: keyEnv, Header: authHeader, Query: authQuery},
				Timeout: config.Duration(timeout),
			}
			if authStyle == channel.StyleNone {
				// 不鉴权时 key_env、header、query 都没有意义，不写进文件。
				record.Auth = config.Auth{Style: authStyle}
			}
			if maxRetries != 0 {
				record.MaxRetries = &maxRetries
			}

			entry := channelEntryOf(record)

			if dryRun {
				return printChannelEntry(cmd, env.ConfigPath, "将要追加的渠道", entry)
			}

			if err := configedit.AppendMapping(env.ConfigPath, []string{"channels"}, entry, reloadableCheck(env.ConfigPath)); err != nil {
				return err
			}

			fmt.Fprintf(out(cmd), "%s %s %s\n",
				palette.program.Render(name),
				palette.ok.Render("已追加"),
				palette.label.Render("("+env.ConfigPath+")"),
			)
			return nil
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&name, "name", "", "渠道名，必须唯一")
	flags.StringVar(&dialectName, "dialect", "", "上游方言：openai、openai_responses、claude 或 gemini")
	flags.StringVar(&baseURL, "base-url", "", "渠道基址，例如 https://api.openai.com")
	flags.StringVar(&path, "path", "", "上游路径，基址带了路径时必须给出")
	flags.StringVar(&keyEnv, "key-env", "", "凭据所在的环境变量名")
	flags.StringVar(&authStyle, "auth-style", "", "鉴权方式：bearer、x-api-key、query、custom 或 none，缺省按 --key-env 推断")
	flags.StringVar(&authHeader, "auth-header", "", "auth-style 为 custom 时的请求头名")
	flags.StringVar(&authQuery, "auth-query", "", "auth-style 为 query 时的查询参数名")
	flags.DurationVar(&timeout, "timeout", 2*time.Minute, "单次请求的整体超时")
	flags.IntVar(&maxRetries, "max-retries", 0, "失败后的重试次数")
	flags.BoolVar(&dryRun, "dry-run", false, "只打印将要写入的条目，不落盘")

	return cmd
}

// coreAuth 把命令行取值装成 core 的鉴权描述。
func coreAuth(style, keyEnv, header, query string) channel.Auth {
	return channel.Auth{Style: style, KeyEnv: keyEnv, Header: header, Query: query}
}

// inChannelAuthStyles 报告鉴权方式是否在允许集合内。
func inChannelAuthStyles(value string) bool {
	for _, candidate := range channelAuthStyles {
		if value == candidate {
			return true
		}
	}
	return false
}

// printChannelEntry 打印一条渠道条目与它要写入的文件，用于 --dry-run。
func printChannelEntry(cmd *cobra.Command, path, title string, entry channelEntry) error {
	data, err := marshalEntry(entry)
	if err != nil {
		return fmt.Errorf("编码渠道条目失败: %w", err)
	}

	writer := out(cmd)
	fmt.Fprintf(writer, "%s %s\n\n", palette.title.Render(title), palette.label.Render("("+path+")"))
	fmt.Fprint(writer, string(data))
	return nil
}

// marshalEntry 把写回条目编码成两格缩进的 YAML，与写进配置时的缩进一致。
func marshalEntry(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := yaml.NewEncoder(&buffer)
	encoder.SetIndent(2)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

// reloadableCheck 返回配置写回后的校验函数：重新加载配置，不合法就报错，由
// configedit 负责还原原文件。channels 与 models 两个命令组共用这一套。
func reloadableCheck(path string) func() error {
	return func() error {
		_, err := config.Load(path)
		return err
	}
}

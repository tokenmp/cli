package cmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/tokenmp/core/config"
	"github.com/tokenmp/core/dialect"
	"github.com/tokenmp/core/server"

	"github.com/tokenmp/cli/internal/logging"
	"github.com/tokenmp/cli/internal/metrics"
)

// testMaxTokens 是最小测试请求允许生成的上限，够拿到一条短回答。
const testMaxTokens = 64

// testDefaultPrompt 是没写 --prompt 时发出去的提示词。
const testDefaultPrompt = "你好"

// testShutdownWait 是请求结束后等待服务退出的上限。Serve 返回时在途请求已经处理完，
// OnRequestDone 也交过结果，这里只是防一次卡死的兜底。
const testShutdownWait = 5 * time.Second

// testSSEDataPrefix 与 testSSEDoneMarker 是 SSE 流里数据行与结束标记的写法。
const (
	testSSEDataPrefix = "data:"
	testSSEDoneMarker = "[DONE]"
)

// testClientDialects 把 --dialect 的取值映射到 core 的方言名。core 的方言名里带
// 下划线的（openai_responses）在这里用短名 responses 暴露。
var testClientDialects = []struct {
	label string
	name  dialect.Name
}{
	{"openai", dialect.OpenAIChat},
	{"responses", dialect.OpenAIResponses},
	{"claude", dialect.ClaudeMessages},
	{"gemini", dialect.GeminiGenerate},
}

// testOptions 是 test 命令的参数。
type testOptions struct {
	// configPath 是配置文件路径。
	configPath string
	// model 是要测试的模型名，为空时取第一条规则的 match。
	model string
	// prompt 是发给上游的提示词。
	prompt string
	// dialect 是客户端方言的短名。
	dialect string
	// stream 按流式发送请求。
	stream bool
	// timeout 是整条请求的超时时间。
	timeout time.Duration
	// raw 打印完整的响应体。
	raw bool
	// json 以 JSON 输出结构化结果。
	json bool
	// verbose 按配置里的级别放开 core 的服务日志。
	verbose bool
}

func newTestCmd() *cobra.Command {
	var opts testOptions

	cmd := &cobra.Command{
		Use:   "test",
		Short: "发一条测试请求，走一遍完整的转发链路",
		Long: `发一条测试请求，走一遍完整的转发链路。

在进程内按配置装配同一套转发服务，监听 127.0.0.1 的随机端口，按选定的客户端方言发
一条最小请求，拿到结果后关掉。选路、跨方言转换、重试与插件都会走一遍，比直接打渠道
更接近真实使用。

先打印配置与候选路由，再打印请求结果：

- 候选路由取自 config.Resolve，与 tokenmp models list --model 试算的是同一份结果，
  上游模型是 ${model} 替换之后的
- 请求结果里的渠道、尝试次数与上游模型取自 core 的 OnRequestDone，TTFT 与 TPS
  的口径复用 internal/metrics

默认把 core 的服务日志压到 warn，只显示结果；--verbose 时按配置里的级别放开。

上游返回非 2xx、超时、装配失败或模型没有匹配规则时以状态码 1 退出，并打印上游错误体。`,
		Example: `  # 用默认方言（openai）打一条
  tokenmp test

  # 指定模型、提示词与客户端方言
  tokenmp test --model deepseek-chat --prompt 你好 --dialect claude

  # 按流式发送，并打印完整的响应体
  tokenmp test --stream --raw

  # 结构化输出，给脚本消费
  tokenmp test --model gpt-4o --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runTest(cmd, opts)
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&opts.configPath, "config", "config.yaml", "配置文件路径")
	flags.StringVar(&opts.model, "model", "", "要测试的模型名，缺省取第一条路由规则的 match")
	flags.StringVar(&opts.prompt, "prompt", testDefaultPrompt, "发给上游的提示词")
	flags.StringVar(&opts.dialect, "dialect", "openai", "客户端方言：openai、responses、claude 或 gemini")
	flags.BoolVar(&opts.stream, "stream", false, "按流式发送请求")
	flags.DurationVar(&opts.timeout, "timeout", 30*time.Second, "整条测试请求的超时时间")
	flags.BoolVar(&opts.raw, "raw", false, "打印完整的响应体")
	flags.BoolVar(&opts.json, "json", false, "以 JSON 输出结构化结果")
	flags.BoolVar(&opts.verbose, "verbose", false, "按配置里的级别打印 core 的服务日志")

	return cmd
}

// clientDialect 是解析后的客户端方言。
type clientDialect struct {
	// label 与 --dialect 的取值一致，用于输出。
	label string
	// name 是 core 的方言名。
	name dialect.Name
}

// parseClientDialect 解析 --dialect，大小写与首尾空白不计。
func parseClientDialect(value string) (clientDialect, error) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	for _, candidate := range testClientDialects {
		if candidate.label == normalized {
			return candidate, nil
		}
	}
	labels := make([]string, 0, len(testClientDialects))
	for _, candidate := range testClientDialects {
		labels = append(labels, candidate.label)
	}
	return clientDialect{}, fmt.Errorf("方言 %q 不是支持的取值，可选 %s", value, joinList(labels))
}

// testOutput 是 test 命令 --json 的输出形状。
type testOutput struct {
	Config   string      `json:"config"`
	Client   string      `json:"client"`
	Endpoint string      `json:"endpoint"`
	Model    string      `json:"model"`
	Prompt   string      `json:"prompt"`
	Stream   bool        `json:"stream"`
	Routes   []routeRow  `json:"routes"`
	Result   *testResult `json:"result,omitempty"`
	// Raw 是 --raw 时的完整响应体。
	Raw string `json:"raw,omitempty"`
}

// testResult 是一次测试请求的结果，字段口径与人类可读输出一致。
type testResult struct {
	Channel   string     `json:"channel"`
	Candidate int        `json:"candidate"`
	Attempts  int        `json:"attempts"`
	Upstream  string     `json:"upstream"`
	Status    int        `json:"status"`
	Duration  int64      `json:"duration_ms"`
	TTFT      *int64     `json:"ttft_ms,omitempty"`
	TPS       *float64   `json:"tps,omitempty"`
	Usage     *testUsage `json:"usage,omitempty"`
	Answer    string     `json:"answer,omitempty"`
	// Error 是非 2xx 或请求失败时的错误文字，非 2xx 时是上游错误体。
	Error string `json:"error,omitempty"`
}

// testUsage 是账目用的用量，字段与 internal/metrics 的口径一致。
type testUsage struct {
	Prompt     int      `json:"prompt_tokens"`
	Completion int      `json:"completion_tokens"`
	Total      int      `json:"total_tokens"`
	CacheRead  int      `json:"cache_read_tokens,omitempty"`
	CacheRate  *float64 `json:"cache_hit_rate,omitempty"`
}

// runTest 装配服务、发一条请求，再把结果打印出来。
func runTest(cmd *cobra.Command, opts testOptions) error {
	client, err := parseClientDialect(opts.dialect)
	if err != nil {
		return err
	}

	outcomes := make(chan server.Outcome, 1)
	collect := func(_ logging.Loggers, outcome server.Outcome) {
		// 只关心第一条：整条命令只发一条测试请求。
		select {
		case outcomes <- outcome:
		default:
		}
	}
	assembled, err := assemble(opts.configPath, cmd.ErrOrStderr(), testLoggers(opts.verbose), collect)
	if err != nil {
		return err
	}

	model := opts.model
	if model == "" {
		if len(assembled.Config.Models) == 0 {
			return errors.New("配置里没有模型路由规则，用 --model 指定要测试的模型")
		}
		model = assembled.Config.Models[0].Match
	}

	endpoint := testEndpoint(client.name, model, opts.stream)
	output := testOutput{
		Config:   assembled.Env.ConfigPath,
		Client:   client.label,
		Endpoint: endpoint,
		Model:    model,
		Prompt:   opts.prompt,
		Stream:   opts.stream,
		Routes:   routeRows(assembled.Config, model),
	}
	if len(output.Routes) == 0 {
		return finishTest(cmd, opts, output, fmt.Errorf("没有匹配模型 %s 的路由", model))
	}

	status, body, requestErr := serveTestRequest(cmd.Context(), assembled, endpoint, client.name, model, opts)

	// Serve 返回时在途请求已经处理完，OnRequestDone 也交过结果；没有请求真正到过
	// 服务时（例如连不上）这里读不到东西，因此不能用阻塞读。
	var outcome server.Outcome
	haveOutcome := false
	select {
	case outcome = <-outcomes:
		haveOutcome = true
	default:
	}

	result := testResultOf(output.Routes, outcome, haveOutcome, status)
	output.Result = &result
	if opts.raw {
		output.Raw = string(body)
	}

	switch {
	case requestErr != nil:
		output.Result.Error = requestErr.Error()
		return finishTest(cmd, opts, output, fmt.Errorf("发送测试请求失败: %w", requestErr))
	case status >= http.StatusBadRequest:
		output.Result.Error = string(body)
		return finishTest(cmd, opts, output, fmt.Errorf("上游返回状态码 %d", status))
	default:
		output.Result.Answer = answerText(client.name, body, opts.stream)
		return finishTest(cmd, opts, output, nil)
	}
}

// serveTestRequest 起服务、发一条请求、再把服务关掉，返回状态码与响应体。
//
// Serve 返回时在途请求已经处理完，OnRequestDone 也交过结果，因此调用方可以在它
// 返回之后再读 outcomes。
func serveTestRequest(
	ctx context.Context,
	assembled *assembly,
	endpoint string,
	client dialect.Name,
	model string,
	opts testOptions,
) (int, []byte, error) {
	// 监听 127.0.0.1 的随机端口，与真实服务走同一条监听与转发路径。
	assembled.Config.Listen = "127.0.0.1:0"
	serverCtx, cancelServer := context.WithCancel(ctx)
	defer cancelServer()

	listener, err := assembled.Service.Listen(serverCtx)
	if err != nil {
		return 0, nil, err
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- assembled.Service.Serve(serverCtx, listener) }()

	status, body, requestErr := sendTestRequest(serverCtx, listener.Addr().String(), endpoint, client, model, opts)

	cancelServer()
	select {
	case <-serveErr:
	case <-time.After(testShutdownWait):
	}
	return status, body, requestErr
}

// sendTestRequest 发一条最小请求，返回状态码与响应体。
func sendTestRequest(
	ctx context.Context,
	address string,
	endpoint string,
	client dialect.Name,
	model string,
	opts testOptions,
) (int, []byte, error) {
	body, err := testRequestBody(client, model, opts.prompt, opts.stream)
	if err != nil {
		return 0, nil, err
	}

	requestCtx, cancel := context.WithTimeout(ctx, opts.timeout)
	defer cancel()

	request, err := http.NewRequestWithContext(requestCtx, http.MethodPost, "http://"+address+endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = response.Body.Close() }()

	payload, readErr := io.ReadAll(response.Body)
	return response.StatusCode, payload, readErr
}

// testRequestBody 按客户端方言构造最小请求体。Gemini 的流式由端点区分，体里不带
// stream 字段；另外三方言写在请求体里。
func testRequestBody(client dialect.Name, model, prompt string, stream bool) ([]byte, error) {
	var body map[string]any
	switch client {
	case dialect.OpenAIChat:
		body = map[string]any{
			"model":      model,
			"messages":   []any{map[string]any{"role": "user", "content": prompt}},
			"max_tokens": testMaxTokens,
		}
	case dialect.OpenAIResponses:
		body = map[string]any{
			"model":             model,
			"input":             prompt,
			"max_output_tokens": testMaxTokens,
		}
	case dialect.ClaudeMessages:
		body = map[string]any{
			"model":      model,
			"max_tokens": testMaxTokens,
			"messages":   []any{map[string]any{"role": "user", "content": prompt}},
		}
	case dialect.GeminiGenerate:
		body = map[string]any{
			"contents":         []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": prompt}}}},
			"generationConfig": map[string]any{"maxOutputTokens": testMaxTokens},
		}
	default:
		return nil, fmt.Errorf("方言 %q 不支持", client)
	}
	if stream && client != dialect.GeminiGenerate {
		body["stream"] = true
	}
	return json.Marshal(body)
}

// testEndpoint 返回客户端要 POST 的本地端点路径。
func testEndpoint(client dialect.Name, model string, stream bool) string {
	if client != dialect.GeminiGenerate {
		return client.LocalPath()
	}
	action := "generateContent"
	if stream {
		action = "streamGenerateContent"
	}
	return "/v1beta/models/" + model + ":" + action
}

// testLoggers 返回 test 命令的日志构造函数：默认把 core 压到 warn，--verbose 放开。
func testLoggers(verbose bool) func(config.Log, io.Writer) logging.Loggers {
	return func(log config.Log, w io.Writer) logging.Loggers {
		if verbose {
			return logging.New(log, w)
		}
		return logging.NewWithCoreLevel(log, w, slog.LevelWarn)
	}
}

// testResultOf 把 core 交出的原始量整理成输出形状。
func testResultOf(routes []routeRow, outcome server.Outcome, have bool, httpStatus int) testResult {
	result := testResult{Status: httpStatus}
	if !have {
		return result
	}

	result.Status = outcome.Status
	result.Duration = outcome.Duration.Milliseconds()
	result.Channel = outcome.Channel
	result.Attempts = len(outcome.Attempts)
	if attempt, ok := lastAttempt(outcome.Attempts); ok {
		result.Upstream = attempt.Upstream
		if result.Channel == "" {
			result.Channel = attempt.Channel
		}
	}
	result.Candidate = candidateOf(routes, result.Channel, result.Upstream)
	if ttft, ok := metrics.TTFT(outcome); ok {
		millis := ttft.Milliseconds()
		result.TTFT = &millis
	}
	if tps, ok := metrics.TPS(outcome); ok {
		rate := tps
		result.TPS = &rate
	}
	if outcome.Usage != nil {
		result.Usage = testUsageOf(*outcome.Usage)
	}
	return result
}

// testUsageOf 把 core 的用量换算成账目字段，缓存命中率复用 metrics 的口径。
func testUsageOf(usage server.Usage) *testUsage {
	row := &testUsage{
		Prompt:     usage.PromptTokens,
		Completion: usage.CompletionTokens,
		Total:      usage.TotalTokens,
		CacheRead:  usage.CachedTokens,
	}
	if rate, ok := metrics.CacheHitRate(usage); ok {
		row.CacheRate = &rate
	}
	return row
}

// lastAttempt 返回最后一次候选尝试。它是决定 Outcome 里渠道与上游模型的那一条。
func lastAttempt(attempts []server.Attempt) (server.Attempt, bool) {
	if len(attempts) == 0 {
		return server.Attempt{}, false
	}
	return attempts[len(attempts)-1], true
}

// candidateOf 返回实际服务的候选在列表里的序号，从 1 开始；找不到时返回 0。
func candidateOf(routes []routeRow, channel, upstream string) int {
	for index, route := range routes {
		if route.Channel == channel && route.Upstream == upstream {
			return index + 1
		}
	}
	return 0
}

// finishTest 打印结果，再按 err 决定退出码。
func finishTest(cmd *cobra.Command, opts testOptions, output testOutput, err error) error {
	if opts.json {
		if jsonErr := printJSON(cmd, output); jsonErr != nil {
			return jsonErr
		}
		return err
	}
	renderTest(cmd, output)
	return err
}

// renderTest 打印配置、候选路由与请求结果。
func renderTest(cmd *cobra.Command, output testOutput) {
	writer := out(cmd)
	printFields(writer, [][2]string{
		{"配置", output.Config},
		{"客户端", output.Client + "（POST " + output.Endpoint + "）"},
		{"模型", output.Model},
	})

	fmt.Fprintln(writer)
	fmt.Fprintln(writer, palette.title.Render("候选路由"))
	if len(output.Routes) == 0 {
		fmt.Fprintf(writer, "%s\n", palette.warn.Render("没有匹配的规则"))
	} else {
		table := newTable().Headers("顺序", "渠道", "上游模型", "方言", "亲和")
		for index, route := range output.Routes {
			table.Row(strconv.Itoa(index+1), route.Channel, route.Upstream, route.Dialect, dash(route.Affinity))
		}
		fmt.Fprintln(writer, table.Render())
	}

	if output.Result == nil {
		return
	}
	fmt.Fprintln(writer)
	fmt.Fprintln(writer, palette.title.Render("请求结果"))
	printFields(writer, testResultFields(*output.Result))

	// --raw 时响应体里已经带上游错误，不再重复打印一次。
	if output.Result.Error != "" && output.Raw == "" {
		fmt.Fprintln(writer)
		fmt.Fprintln(writer, palette.fail.Render("上游错误"))
		fmt.Fprintln(writer, output.Result.Error)
	}
	if output.Raw != "" {
		fmt.Fprintln(writer)
		fmt.Fprintln(writer, palette.title.Render("响应体"))
		fmt.Fprintln(writer, output.Raw)
	}
}

// testResultFields 把结果整理成标签与值对齐的字段表。
func testResultFields(result testResult) [][2]string {
	rows := [][2]string{
		{"渠道", channelText(result)},
		{"上游模型", dash(result.Upstream)},
		{"状态", statusText(result.Status)},
		{"耗时", formatMillis(result.Duration)},
	}
	if result.TTFT != nil {
		rows = append(rows, [2]string{"TTFT", formatMillis(*result.TTFT)})
	}
	if result.TPS != nil {
		rows = append(rows, [2]string{"TPS", strconv.FormatFloat(*result.TPS, 'f', 1, 64)})
	}
	if result.Usage != nil {
		rows = append(rows, [2]string{"用量", usageText(*result.Usage)})
	}
	if result.Answer != "" {
		rows = append(rows, [2]string{"回答", result.Answer})
	}
	return rows
}

// channelText 说明结果来自哪条渠道、第几条候选、试了几次。
func channelText(result testResult) string {
	if result.Channel == "" {
		return "-"
	}
	if result.Candidate > 0 {
		return fmt.Sprintf("%s（第 %d 条候选，尝试 %d 次）", result.Channel, result.Candidate, result.Attempts)
	}
	return fmt.Sprintf("%s（尝试 %d 次）", result.Channel, result.Attempts)
}

// statusText 渲染状态码，拿不到状态码时给占位符。
func statusText(status int) string {
	if status == 0 {
		return "-"
	}
	return strconv.Itoa(status)
}

// formatMillis 把毫秒渲染成时长，一秒以内保留到毫秒，以上保留到十分之一秒。
func formatMillis(millis int64) string {
	duration := time.Duration(millis) * time.Millisecond
	if duration < time.Second {
		return duration.String()
	}
	return duration.Round(100 * time.Millisecond).String()
}

// usageText 按账目行的口径把用量拼成一行。
func usageText(usage testUsage) string {
	parts := []string{
		fmt.Sprintf("prompt=%d", usage.Prompt),
		fmt.Sprintf("completion=%d", usage.Completion),
		fmt.Sprintf("total=%d", usage.Total),
	}
	if usage.CacheRead > 0 {
		parts = append(parts, fmt.Sprintf("cache_read=%d", usage.CacheRead))
	}
	if usage.CacheRate != nil {
		parts = append(parts, "cache_hit_rate="+strconv.FormatFloat(*usage.CacheRate, 'f', 3, 64))
	}
	return strings.Join(parts, " ")
}

// answerText 按客户端方言从响应体里取出回答文本。取不到时返回空串，不报错。
func answerText(client dialect.Name, body []byte, stream bool) string {
	if len(bytes.TrimSpace(body)) == 0 {
		return ""
	}
	if stream {
		return streamAnswer(client, body)
	}
	switch client {
	case dialect.OpenAIChat:
		return openAIChatAnswer(body)
	case dialect.OpenAIResponses:
		return responsesAnswer(body)
	case dialect.ClaudeMessages:
		return claudeAnswer(body)
	case dialect.GeminiGenerate:
		return geminiAnswer(body)
	default:
		return ""
	}
}

// openAIChatAnswer 取 OpenAI Chat 响应里各条 choice 的正文。
func openAIChatAnswer(body []byte) string {
	var payload struct {
		Choices []struct {
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	var text strings.Builder
	for _, choice := range payload.Choices {
		text.WriteString(contentText(choice.Message.Content))
	}
	return text.String()
}

// responsesAnswer 取 OpenAI Responses 响应里的输出文本。
func responsesAnswer(body []byte) string {
	var payload struct {
		Output []struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
		OutputText string `json:"output_text"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	var text strings.Builder
	for _, item := range payload.Output {
		for _, block := range item.Content {
			if block.Type == "output_text" {
				text.WriteString(block.Text)
			}
		}
	}
	if text.Len() > 0 {
		return text.String()
	}
	return payload.OutputText
}

// claudeAnswer 取 Claude Messages 响应里的文本块。
func claudeAnswer(body []byte) string {
	var payload struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	var text strings.Builder
	for _, block := range payload.Content {
		if block.Type == "" || block.Type == "text" {
			text.WriteString(block.Text)
		}
	}
	return text.String()
}

// geminiAnswer 取 Gemini 响应里候选内容的各部分文本。
func geminiAnswer(body []byte) string {
	return geminiText(body)
}

// geminiText 取 Gemini 响应或流式分片里候选内容的文本。
func geminiText(body []byte) string {
	var payload struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	var text strings.Builder
	for _, candidate := range payload.Candidates {
		for _, part := range candidate.Content.Parts {
			text.WriteString(part.Text)
		}
	}
	return text.String()
}

// contentText 取一段消息内容：字符串直接返回，内容块数组把文本块拼起来。
func contentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	var blocks []struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err == nil {
		var joined strings.Builder
		for _, block := range blocks {
			joined.WriteString(block.Text)
		}
		return joined.String()
	}
	return ""
}

// streamAnswer 从 SSE 流里按客户端方言把增量文本拼起来。
func streamAnswer(client dialect.Name, body []byte) string {
	var text strings.Builder
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, testSSEDataPrefix) {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, testSSEDataPrefix))
		if payload == "" || payload == testSSEDoneMarker {
			continue
		}
		text.WriteString(streamChunkAnswer(client, []byte(payload)))
	}
	return text.String()
}

// streamChunkAnswer 从一条流式分片里取增量文本。
func streamChunkAnswer(client dialect.Name, payload []byte) string {
	switch client {
	case dialect.OpenAIChat:
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content json.RawMessage `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal(payload, &chunk); err != nil {
			return ""
		}
		var text strings.Builder
		for _, choice := range chunk.Choices {
			text.WriteString(contentText(choice.Delta.Content))
		}
		return text.String()
	case dialect.OpenAIResponses:
		var chunk struct {
			Type  string `json:"type"`
			Delta string `json:"delta"`
		}
		if err := json.Unmarshal(payload, &chunk); err != nil {
			return ""
		}
		if chunk.Type == "response.output_text.delta" {
			return chunk.Delta
		}
		return ""
	case dialect.ClaudeMessages:
		var chunk struct {
			Type  string `json:"type"`
			Delta struct {
				Text string `json:"text"`
			} `json:"delta"`
		}
		if err := json.Unmarshal(payload, &chunk); err != nil {
			return ""
		}
		if chunk.Type == "content_block_delta" {
			return chunk.Delta.Text
		}
		return ""
	case dialect.GeminiGenerate:
		return geminiText(payload)
	default:
		return ""
	}
}

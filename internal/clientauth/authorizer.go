package clientauth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/tokenmp/core/dialect"
)

// 校验请求的参数。都在这里写一次。
const (
	// lookupTimeout 是单次校验请求的超时。它在转发链路上，压短一点：端点数分钟不通
	// 时，fail_closed 下的客户端每次最多等这么久。
	lookupTimeout = 2 * time.Second
	// lookupAttempts 是校验请求的尝试次数。第一次失败后隔 lookupRetryDelay 再试一次，
	// 单个瞬时抖动不值得把请求拒掉；再多就会让 fail_closed 下的客户端等太久。
	lookupAttempts = 2
	// lookupRetryDelay 是两次尝试之间的等待。
	lookupRetryDelay = 200 * time.Millisecond
	// maxErrorBodyBytes 是读进日志的校验端点错误体上限。
	maxErrorBodyBytes = 8 << 10
	// defaultCacheTTL 是调用方没给缓存周期时的兜底。
	defaultCacheTTL = time.Minute
)

// errNotRetryable 标记再试也不会变好的响应，例如密钥校验端点返回 400 或 401。
var errNotRetryable = errors.New("clientauth: 校验端点拒绝")

// Options 是 Authorizer 的装配参数。
type Options struct {
	// Endpoint 是密钥校验端点地址，必填。
	Endpoint string
	// Secret 是内部端点共享密钥的值，写进 x-callback-secret。
	Secret string
	// CacheTTL 是一把密钥的校验结果缓存多久，非正数时用 defaultCacheTTL。
	CacheTTL time.Duration
	// FailClosed 为真时校验端点不通就拒绝请求，为假时放行但不带身份。
	FailClosed bool
	// Client 是发校验请求用的 HTTP 客户端，为 nil 时用带 lookupTimeout 的新客户端。
	Client *http.Client
	// Now 返回当前时间，为 nil 时用 time.Now。
	Now func() time.Time
	// CacheSize 是缓存能放的密钥数上限，非正数时用 defaultCacheSize。
	CacheSize int
}

// Authorizer 认出调用方身份：取密钥、调端点校验、按哈希缓存、按结果放行或拒绝。
type Authorizer struct {
	endpoint   string
	secret     string
	ttl        time.Duration
	failClosed bool
	logger     *slog.Logger
	client     *http.Client
	now        func() time.Time

	cache *cache
	// group 让同一把密钥的并发请求只打一次校验端点。
	group singleflight.Group

	// rejected 与 unreachable 是给日志用的累计计数。端点不通是运维要看的信号，
	// 不能静默，日志里带一条当前总数，便于和别的指标对上。
	rejected    atomic.Int64
	unreachable atomic.Int64
}

// New 构造校验器。
func New(opts Options, logger *slog.Logger) *Authorizer {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	client := opts.Client
	if client == nil {
		client = &http.Client{Timeout: lookupTimeout}
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	ttl := opts.CacheTTL
	if ttl <= 0 {
		ttl = defaultCacheTTL
	}
	return &Authorizer{
		endpoint:   opts.Endpoint,
		secret:     opts.Secret,
		ttl:        ttl,
		failClosed: opts.FailClosed,
		logger:     logger,
		client:     client,
		now:        now,
		cache:      newCache(opts.CacheSize, now),
	}
}

// Middleware 返回宿主中间件，装进 core 的 server.WithMiddleware。
//
// 只拦模型端点的 POST，含 Claude Messages 的计数端点；/healthz、/v1/models 与未知路径
// 原样交给 core。核验通过就把身份挂进 context 再调 next；不通过就自己写错误响应、不调 next。
func (a *Authorizer) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		name, ok := dialectOfPath(request.URL.Path)
		if !ok || request.Method != http.MethodPost {
			next.ServeHTTP(writer, request)
			return
		}
		key, ok := extractKey(name, request)
		if !ok {
			a.rejectClient(writer, request, http.StatusUnauthorized, "invalid_api_key", "缺少密钥")
			return
		}
		result, err := a.verify(request.Context(), key)
		if err != nil {
			if a.rejectUnreachable(writer, request, err) {
				return
			}
			// fail_closed 为假：放行但不带身份，这条请求的记录里两列为空。
			next.ServeHTTP(writer, request)
			return
		}
		if result.rejected() {
			a.logRejected(key, result)
			// 对外只回 401：校验端点把密钥不存在 / 停用 / 过期分成 404 / 403 / 410，那是给
			// 内核排查用的。传给客户端就不对了 —— 404 看起来像路径不存在，各家 SDK 不会
			// 按认证失败处理，不会提示重新填密钥。具体是哪一类留在上面那条 WARN 里。
			writeError(writer, http.StatusUnauthorized, "invalid_api_key", result.rejectMessage)
			return
		}
		if !allowedModel(name, request, result.whitelist) {
			a.rejectClient(writer, request, http.StatusForbidden, "forbidden", "该模型不在密钥的可用范围内")
			return
		}
		next.ServeHTTP(writer, request.WithContext(WithIdentity(request.Context(), result.identity)))
	})
}

// allowedModel 报告请求的模型是否在密钥的白名单里。白名单为空表示不限制；取不到模型
// 时放行，让 core 去报请求体不合法，不在这里替它下结论。
func allowedModel(name dialect.Name, request *http.Request, whitelist []string) bool {
	if len(whitelist) == 0 {
		return true
	}
	model := modelOfRequest(name, request)
	if model == "" {
		return true
	}
	for _, allowed := range whitelist {
		if allowed == model {
			return true
		}
	}
	return false
}

// verify 取一把密钥的校验结果：先查缓存，未命中再打端点。同一把密钥的并发请求只打
// 一次端点。返回错误表示端点不可用，由调用方按 fail_closed 决定放行还是拒绝。
func (a *Authorizer) verify(ctx context.Context, key string) (verification, error) {
	digest := sha256.Sum256([]byte(key))
	if cached, ok := a.cache.get(digest); ok {
		return cached, nil
	}
	// 单飞之后还要再查一次缓存：等在单飞里的请求拿到的可能就是别人刚写进去的。
	shared, err, _ := a.group.Do(string(digest[:]), func() (any, error) {
		if cached, ok := a.cache.get(digest); ok {
			return cached, nil
		}
		result, err := a.lookup(ctx, key)
		if err != nil {
			return verification{}, err
		}
		ttl := a.ttl
		if result.expiresAt != nil {
			if untilExpiry := result.expiresAt.Sub(a.now()); untilExpiry < ttl {
				ttl = untilExpiry
			}
		}
		if ttl > 0 {
			a.cache.put(digest, result, ttl)
		}
		return result, nil
	})
	if err != nil {
		return verification{}, err
	}
	return shared.(verification), nil
}

// lookup 打校验端点，可重试的错误按固定间隔再试一次。
func (a *Authorizer) lookup(ctx context.Context, key string) (verification, error) {
	payload, err := json.Marshal(struct {
		Key string `json:"key"`
	}{Key: key})
	if err != nil {
		return verification{}, err
	}
	var lastErr error
	for attempt := 0; attempt < lookupAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(lookupRetryDelay):
			case <-ctx.Done():
				return verification{}, ctx.Err()
			}
		}
		result, err := a.attempt(ctx, payload)
		if err == nil {
			return result, nil
		}
		if errors.Is(err, errNotRetryable) {
			return verification{}, err
		}
		lastErr = err
	}
	return verification{}, lastErr
}

// attempt 发一次校验请求，把响应翻成校验结果。
func (a *Authorizer) attempt(ctx context.Context, payload []byte) (verification, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, a.endpoint, bytes.NewReader(payload))
	if err != nil {
		return verification{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("x-callback-secret", a.secret)

	response, err := a.client.Do(request)
	if err != nil {
		return verification{}, err
	}
	defer func() { _ = response.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(response.Body, maxErrorBodyBytes))

	switch response.StatusCode {
	case http.StatusOK:
		return decodeSuccess(body)
	case http.StatusNotFound:
		return rejection(http.StatusNotFound, "invalid_api_key", "密钥不存在", body), nil
	case http.StatusForbidden:
		// 403 有两种来源：密钥或账号停用，以及本机共享密钥写错。后者只体现在响应文字
		// 里，统一按「密钥不可用」拒绝，原因文字进日志，不回给客户端。
		return rejection(http.StatusForbidden, "forbidden", "密钥或账号已停用", body), nil
	case http.StatusGone:
		return rejection(http.StatusGone, "key_expired", "密钥已过期", body), nil
	default:
		err := fmt.Errorf("校验端点返回状态码 %d", response.StatusCode)
		if response.StatusCode >= 500 || response.StatusCode == http.StatusTooManyRequests {
			return verification{}, err
		}
		// 4xx 说明本机装配或密钥配错了，重试不会变好，也别把锅甩给客户端。
		return verification{}, fmt.Errorf("%w: %v", errNotRetryable, err)
	}
}

// lookupResponse 是校验端点的成功响应形状。已知字段之外的 JSON 会被忽略，不影响校验。
type lookupResponse struct {
	OK   bool `json:"ok"`
	Data *struct {
		UserID          int64          `json:"user_id"`
		APIKeyID        int64          `json:"api_key_id"`
		ModelWhitelist  []string       `json:"model_whitelist"`
		RateLimitPerMin *int           `json:"rate_limit_per_min"`
		ExpiresAt       *time.Time     `json:"expires_at"`
		Pricing         *lookupPricing `json:"pricing"`
	} `json:"data"`
}

// lookupPricing 是校验端点响应里的计价口径。字段都可空：没有套餐的用户三个标量都
// 是 null，prices 是空数组。
type lookupPricing struct {
	PlanID           *int64        `json:"plan_id"`
	PricingMode      *string       `json:"pricing_mode"`
	MarkupMultiplier *float64      `json:"markup_multiplier"`
	Prices           []lookupPrice `json:"prices"`
}

// lookupPrice 是计价口径里一个模型的价。
type lookupPrice struct {
	Model                string  `json:"model"`
	CostMode             string  `json:"cost_mode"`
	InputPerMillion      float64 `json:"input_per_million"`
	OutputPerMillion     float64 `json:"output_per_million"`
	CacheReadPerMillion  float64 `json:"cache_read_per_million"`
	CacheWritePerMillion float64 `json:"cache_write_per_million"`
	PerRequest           float64 `json:"per_request"`
	RequestWeight        float64 `json:"request_weight"`
}

// decodeSuccess 把 200 的响应翻成校验结果。形状对不上时按端点不可用处理。
func decodeSuccess(body []byte) (verification, error) {
	var decoded lookupResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return verification{}, fmt.Errorf("解析校验端点响应失败: %w", err)
	}
	if !decoded.OK || decoded.Data == nil {
		return verification{}, errors.New("校验端点返回 200 但没有身份数据")
	}
	return verification{
		identity: Identity{
			UserID:   decoded.Data.UserID,
			APIKeyID: decoded.Data.APIKeyID,
			Pricing:  pricingOf(decoded.Data.Pricing),
		},
		whitelist:       decoded.Data.ModelWhitelist,
		expiresAt:       decoded.Data.ExpiresAt,
		rateLimitPerMin: decoded.Data.RateLimitPerMin,
	}, nil
}

// pricingOf 把响应里的 pricing 段翻成 Pricing。整段缺失时返回零值。
func pricingOf(raw *lookupPricing) Pricing {
	if raw == nil {
		return Pricing{}
	}
	pricing := Pricing{PlanID: raw.PlanID}
	if raw.PricingMode != nil {
		pricing.Mode = *raw.PricingMode
	}
	if raw.MarkupMultiplier != nil {
		pricing.MarkupMultiplier = *raw.MarkupMultiplier
	}
	if len(raw.Prices) > 0 {
		pricing.Prices = make(map[string]Price, len(raw.Prices))
		for _, item := range raw.Prices {
			pricing.Prices[item.Model] = Price{
				CostMode:             item.CostMode,
				InputPerMillion:      item.InputPerMillion,
				OutputPerMillion:     item.OutputPerMillion,
				CacheReadPerMillion:  item.CacheReadPerMillion,
				CacheWritePerMillion: item.CacheWritePerMillion,
				PerRequest:           item.PerRequest,
				RequestWeight:        item.RequestWeight,
			}
		}
	}
	return pricing
}

// rejection 把失败响应翻成被拒的校验结果。body 里的原因文字只收进 detail，供日志用。
func rejection(status int, code, message string, body []byte) verification {
	return verification{
		rejectStatus:  status,
		rejectCode:    code,
		rejectMessage: message,
		detail:        errorMessage(body),
	}
}

// errorMessage 从校验端点的错误体 {"error":"..."} 里取出原因文字。取不到时返回空串。
func errorMessage(body []byte) string {
	var decoded struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		return ""
	}
	return decoded.Error
}

// rejectClient 写出一条客户端错误响应并记一条 WARN。
func (a *Authorizer) rejectClient(writer http.ResponseWriter, request *http.Request, status int, code, message string) {
	count := a.rejected.Add(1)
	a.logger.Warn("客户端密钥不合规，拒绝请求",
		"path", request.URL.Path,
		"status", status,
		"code", code,
		"rejected_total", count)
	writeError(writer, status, code, message)
}

// logRejected 记一条校验端点给出的拒绝。
// 状态码与错误码取校验端点给的那一份（404 / 403 / 410 与各自的 code），
// 对外回的是统一的 401，两者的差别只体现在这里。
func (a *Authorizer) logRejected(key string, result verification) {
	count := a.rejected.Add(1)
	a.logger.Warn("客户端密钥校验未通过，拒绝请求",
		"key", fingerprint(key),
		"status", result.rejectStatus,
		"code", result.rejectCode,
		"reason", result.detail,
		"rejected_total", count)
}

// rejectUnreachable 处理校验端点不可用。fail_closed 为真时写出错误响应并返回真；
// 为假时只记日志并返回假，由调用方放行。
func (a *Authorizer) rejectUnreachable(writer http.ResponseWriter, request *http.Request, err error) bool {
	count := a.unreachable.Add(1)
	if !a.failClosed {
		a.logger.Warn("客户端密钥校验端点不可用，按配置放行请求，本次不带身份",
			"path", request.URL.Path,
			"endpoint", a.endpoint,
			"error", err.Error(),
			"unreachable_total", count)
		return false
	}
	a.logger.Warn("客户端密钥校验端点不可用，按配置拒绝请求",
		"path", request.URL.Path,
		"endpoint", a.endpoint,
		"error", err.Error(),
		"unreachable_total", count)
	writeError(writer, http.StatusServiceUnavailable, "identity_unavailable", "身份校验服务不可用，请稍后重试")
	return true
}

// fingerprint 返回密钥的短哈希，只用于日志。明文密钥不进日志。
func fingerprint(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:8])
}

// writeError 按 core 的 writeError 同一形状写出错误响应，不另造一种。
//
// core 没有导出那个函数，这里照写一份：形状是 {"error":{"message","type","code"}}，
// type 与 code 同取错误码。改 core 的形状时这里要跟着改。
func writeError(writer http.ResponseWriter, status int, code, message string) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	payload := map[string]any{
		"error": map[string]any{
			"message": message,
			"type":    code,
			"code":    code,
		},
	}
	_ = json.NewEncoder(writer).Encode(payload)
}

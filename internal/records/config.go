// Package records 把 core 交出的调用量整理成 web 的 request_records 一行，并攒批推给
// web 的摄入端点。
//
// 推送是旁路：攒批在内存里做，发送失败只影响队列，不挡转发。身份由 internal/clientauth
// 在转发路径上认出来（宿主的中间件把结果填进 context），金额现在还没有来源，字段留空，
// 原因写在 Row 上。
package records

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/tokenmp/core/config"
)

// 默认值与上限。只在这里写一次，校验与文档都以它为准。
const (
	// DefaultBatchSize 是一批最多的行数，与对接文档里「一批几百行」一致。
	DefaultBatchSize = 200
	// DefaultFlushInterval 是攒不满一批时的兜底发送间隔。
	DefaultFlushInterval = 5 * time.Second
	// DefaultQueueSize 是内存队列能放的行数上限。满了丢最旧的一条：比丢最新的一条
	// 更接近「保留最近发生的事」，也与对接文档里失败重发整批的语义合得上。
	DefaultQueueSize = 10000
	// DefaultCacheTTL 是客户端密钥校验结果的缓存周期。
	DefaultCacheTTL = time.Minute
)

// Config 是配置文件里的 records 段。整段不写时不推送也不校验密钥，tokenmp run 的
// 行为与没有它时一样。
type Config struct {
	// Endpoint 是 web 的摄入端点地址。留空表示不推送。
	Endpoint string `yaml:"endpoint"`
	// SecretEnv 是共享密钥所在的环境变量名。密钥值只在启动时读一次，不写进配置文件，
	// 与渠道凭据同一规矩。
	SecretEnv string `yaml:"secret_env"`
	// BatchSize 是一批最多的行数，不写用 DefaultBatchSize。
	BatchSize int `yaml:"batch_size"`
	// FlushInterval 是攒不满一批时的兜底发送间隔，不写用 DefaultFlushInterval。
	FlushInterval config.Duration `yaml:"flush_interval"`

	// AuthEndpoint 是客户端密钥校验端点。留空时从 Endpoint 推出：两个端点由 web
	// 固定，把结尾的 /records 换成 /api-key 就得到它，不必让使用者抄两遍地址。
	AuthEndpoint string `yaml:"auth_endpoint"`
	// CacheTTL 是一把密钥的校验结果缓存多久，不写用 DefaultCacheTTL。
	CacheTTL config.Duration `yaml:"cache_ttl"`
	// FailClosed 决定校验端点不通时是拒绝还是放行。不写按 true 拒绝：放行会让鉴权
	// 在这段时间形同虚设，拒绝有下面这条 WARN 日志与计数可查。
	FailClosed *bool `yaml:"fail_closed"`
}

// Enabled 报告 records 段是否配好了端点。为假时宿主不推送，也不装客户端密钥校验。
func (c *Config) Enabled() bool {
	return c != nil && strings.TrimSpace(c.Endpoint) != ""
}

// Defaults 填入未写的默认值。校验前调用，与 core 的 Config.Defaults 同一时机。
func (c *Config) Defaults() {
	if c.BatchSize <= 0 {
		c.BatchSize = DefaultBatchSize
	}
	if c.FlushInterval <= 0 {
		c.FlushInterval = config.Duration(DefaultFlushInterval)
	}
	if c.CacheTTL <= 0 {
		c.CacheTTL = config.Duration(DefaultCacheTTL)
	}
}

// AuthURL 返回客户端密钥校验端点的地址。
//
// 没显式写 auth_endpoint 时从摄入端点推：web 的两个内部端点路径固定，把结尾的
// /records 换成 /api-key，就得到同一台机器上的校验端点。
func (c *Config) AuthURL() string {
	if trimmed := strings.TrimSpace(c.AuthEndpoint); trimmed != "" {
		return trimmed
	}
	base := strings.TrimRight(strings.TrimSpace(c.Endpoint), "/")
	if strings.HasSuffix(base, "/records") {
		return strings.TrimSuffix(base, "/records") + "/api-key"
	}
	return base + "/api-key"
}

// FailClosedEnabled 报告校验端点不通时是否拒绝。没写 fail_closed 时按 true。
func (c *Config) FailClosedEnabled() bool {
	return c.FailClosed == nil || *c.FailClosed
}

// Validate 校验 records 段。整段存在时端点与密钥变量名都必填。
//
// 校验在 core 的 Config.Validate 之外单独做：core 不认识这一段，把问题并进它的多问题
// 列表就得改另一个仓库，先各自报各自的。
func (c *Config) Validate() error {
	var problems []string
	endpoint := strings.TrimSpace(c.Endpoint)
	switch {
	case endpoint == "":
		problems = append(problems, "endpoint 不能为空")
	case !strings.HasPrefix(endpoint, "http://") && !strings.HasPrefix(endpoint, "https://"):
		problems = append(problems, "endpoint 必须是 http:// 或 https:// 开头")
	}
	if strings.TrimSpace(c.SecretEnv) == "" {
		problems = append(problems, "secret_env 不能为空")
	}
	if c.BatchSize <= 0 {
		problems = append(problems, "batch_size 必须为正")
	}
	if c.FlushInterval <= 0 {
		problems = append(problems, "flush_interval 必须为正")
	}
	if auth := strings.TrimSpace(c.AuthEndpoint); auth != "" &&
		!strings.HasPrefix(auth, "http://") && !strings.HasPrefix(auth, "https://") {
		problems = append(problems, "auth_endpoint 必须是 http:// 或 https:// 开头")
	}
	if c.CacheTTL <= 0 {
		problems = append(problems, "cache_ttl 必须为正")
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("records: %s", strings.Join(problems, "; "))
}

// SecretFromEnv 从 secret_env 指的环境变量读取共享密钥。
//
// 密钥值只从环境变量读，与渠道凭据同一规矩：配置文件里只写变量名，值不落盘。
// 变量名没配或变量为空都算配置不完整，与渠道缺凭据一样在启动时报错。
func (c *Config) SecretFromEnv() (string, error) {
	if strings.TrimSpace(c.SecretEnv) == "" {
		return "", errors.New("records.secret_env 没有配置")
	}
	value := os.Getenv(c.SecretEnv)
	if value == "" {
		return "", fmt.Errorf("records.secret_env 指向的环境变量 %s 没有设置", c.SecretEnv)
	}
	return value, nil
}

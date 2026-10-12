package clientauth

import (
	"crypto/sha256"
	"sync"
	"time"
)

// defaultCacheSize 是缓存能放的密钥数上限。
//
// 缓存键是密钥的 sha256，不能无限增长：攻击者可以拿随机密钥把表撑大。到上限时先清
// 过期的，还满就随便丢一条，宁可让某把密钥多打一次校验端点，也不让内存跟着请求量走。
const defaultCacheSize = 4096

// verification 是一次校验的结果。被拒与被通过共用一种结构：两者都按同样的周期缓存，
// 这样同一把无效密钥连续打过来不会每次都去问端点。
type verification struct {
	// rejectStatus 非 0 表示校验没通过，取值是回给客户端的状态码。
	rejectStatus  int
	rejectCode    string
	rejectMessage string
	// detail 是校验端点给出的原因文字，只进日志，不回给客户端。
	detail string

	identity Identity
	// whitelist 为空表示不限制模型。
	whitelist []string
	// expiresAt 是密钥的过期时间，用来把缓存周期截到过期那一刻。
	expiresAt *time.Time

	// rateLimitPerMin 收进来但本 issue 不启用：core 现在没有客户端限速，限速是另一
	// 件事。收进缓存结构是为了限速落地时不必再改一次校验的接口。
	rateLimitPerMin *int
}

// rejected 报告这次校验是否是被拒的。
func (v verification) rejected() bool { return v.rejectStatus != 0 }

// cacheEntry 是缓存里的一条。
type cacheEntry struct {
	value   verification
	expires time.Time
}

// cache 是按密钥哈希存校验结果的带期限表。键是明文的 sha256，不存明文。
type cache struct {
	mu      sync.Mutex
	entries map[[sha256.Size]byte]cacheEntry
	max     int
	now     func() time.Time
}

func newCache(max int, now func() time.Time) *cache {
	if max <= 0 {
		max = defaultCacheSize
	}
	return &cache{
		entries: make(map[[sha256.Size]byte]cacheEntry),
		max:     max,
		now:     now,
	}
}

// get 取一条还没过期的结果。
func (c *cache) get(key [sha256.Size]byte) (verification, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok || !entry.expires.After(c.now()) {
		if ok {
			delete(c.entries, key)
		}
		return verification{}, false
	}
	return entry.value, true
}

// put 写入一条结果，ttl 之后过期。
func (c *cache) put(key [sha256.Size]byte, value verification, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= c.max {
		c.evictLocked()
	}
	c.entries[key] = cacheEntry{value: value, expires: c.now().Add(ttl)}
}

// evictLocked 腾位置：先清过期的，还是满的就随便丢一条。调用方持锁。
func (c *cache) evictLocked() {
	now := c.now()
	for key, entry := range c.entries {
		if !entry.expires.After(now) {
			delete(c.entries, key)
		}
	}
	if len(c.entries) < c.max {
		return
	}
	for key := range c.entries {
		delete(c.entries, key)
		return
	}
}

package records

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/tokenmp/cli/internal/billing"
)

// 退避与超时参数。都在这里写一次。
const (
	// initialBackoff 是第一次重试前的等待时长，之后翻倍。
	initialBackoff = 1 * time.Second
	// maxBackoff 是指数退避的上限。端点数分钟不通时不必更密地重试。
	maxBackoff = 30 * time.Second
	// requestTimeout 是单次 POST 的超时。它比退避上限小，一次失败不会拖住整批太久。
	requestTimeout = 10 * time.Second
)

// errNotRetryable 标记重试也不会变好的响应，例如 401 与 400。
var errNotRetryable = errors.New("records: 摄入端点拒绝")

// Sender 攒批把调用记录推给摄入端点。
//
// 转发路径只调 Add，它只往内存队列里放，不碰网络；发送与重试都在后台一条 goroutine
// 上跑，因此记账慢不会让请求排队。队列有上限，满了丢最旧的一条。
type Sender struct {
	endpoint      string
	secret        string
	batchSize     int
	flushInterval time.Duration
	queueSize     int
	initialDelay  time.Duration
	maxDelay      time.Duration
	logger        *slog.Logger
	client        *http.Client

	mu      sync.Mutex
	queue   []Row
	billing []billing.Row
	closed  bool
	dropped int64
	// droppedBilling 单独计数：两类行各有各的容量，丢掉的原因分开看。
	droppedBilling int64

	wake chan struct{}
	// quit 把停机时的冲刷截止时间交给后台 goroutine，缓冲一格让 Close 不必等它。
	quit      chan context.Context
	done      chan struct{}
	closeOnce sync.Once

	// sendCtx 在停机时取消，用来打断正在重试的一批；它不决定那一批是否丢弃，
	// run 把没送出的那批留到最后的冲刷里再试一次。
	sendCtx    context.Context
	cancelSend context.CancelFunc
}

// New 按配置构造发送端并启动后台发送。secret 是共享密钥的值，由调用方从环境变量
// 读出；logger 为空时丢弃日志。
func New(cfg Config, secret string, logger *slog.Logger) *Sender {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	ctx, cancel := context.WithCancel(context.Background())
	sender := &Sender{
		endpoint:      cfg.Endpoint,
		secret:        secret,
		batchSize:     cfg.BatchSize,
		flushInterval: cfg.FlushInterval.Duration(),
		queueSize:     DefaultQueueSize,
		initialDelay:  initialBackoff,
		maxDelay:      maxBackoff,
		logger:        logger,
		client:        &http.Client{Timeout: requestTimeout},
		wake:          make(chan struct{}, 1),
		quit:          make(chan context.Context, 1),
		done:          make(chan struct{}),
		sendCtx:       ctx,
		cancelSend:    cancel,
	}
	go sender.run()
	return sender
}

// Add 把一行调用记录放进队列。队列满时丢最旧的一条并记 WARN。
//
// 它不阻塞：转发路径调它时只拿一次锁。
func (s *Sender) Add(row Row) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	dropped := false
	droppedTotal := int64(0)
	if len(s.queue) >= s.queueSize {
		s.queue = s.queue[1:]
		s.dropped++
		droppedTotal = s.dropped
		dropped = true
	}
	s.queue = append(s.queue, row)
	size := len(s.queue)
	s.mu.Unlock()

	if dropped {
		s.logger.Warn("调用记录队列已满，丢弃最旧的一行", "queue_size", s.queueSize, "dropped", droppedTotal)
	}
	if size >= s.batchSize {
		s.signal()
	}
}

// AddBilling 把一行计费放进队列。它与 Add 共用一条发送循环与同一批 POST，队列满了
// 同样丢最旧的一条。
//
// 每来一行就叫一次发送循环，不用等 records 攒够一批：计费行少，早点发出去对账更及时。
func (s *Sender) AddBilling(row billing.Row) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	dropped := false
	droppedTotal := int64(0)
	if len(s.billing) >= s.queueSize {
		s.billing = s.billing[1:]
		s.droppedBilling++
		droppedTotal = s.droppedBilling
		dropped = true
	}
	s.billing = append(s.billing, row)
	s.mu.Unlock()

	if dropped {
		s.logger.Warn("计费记录队列已满，丢弃最旧的一行", "queue_size", s.queueSize, "dropped", droppedTotal)
	}
	s.signal()
}

// Close 停止接收新行，把队列里剩下的发完再返回。timeout 是冲刷的上限，超过就放弃
// 并记 WARN。重复调用只生效一次。
func (s *Sender) Close(timeout time.Duration) {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		// 先打断正在重试的那一批，run 收到 quit 之后会连它带队列一起再送一次。
		s.cancelSend()
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		s.quit <- ctx
		<-s.done
	})
}

// run 是后台发送循环。平时按批大小或兜底间隔取一批发送；收到 quit 之后把手里与
// 队列里剩下的都送完。
func (s *Sender) run() {
	ticker := time.NewTicker(s.flushInterval)
	defer ticker.Stop()
	defer close(s.done)

	// pending 是已经取出来但还没送出的一批。发送被打断时留着它，停机时先发它，
	// 这样取消 sendCtx 不会把手里这批丢掉。
	var pending batch
	for {
		select {
		case ctx := <-s.quit:
			s.finish(ctx, pending)
			return
		case <-ticker.C:
		case <-s.wake:
		}

		if pending.empty() {
			pending = s.take(s.batchSize)
		}
		if pending.empty() {
			continue
		}
		if err := s.deliver(s.sendCtx, pending); err == nil {
			pending = batch{}
			// 队列里还够一批就立刻接着发，不必等下一次兜底间隔。
			if s.size() >= s.batchSize {
				s.signal()
			}
		}
	}
}

// finish 是停机时的收尾：先补发手里这批，再把队列排空。都按 ctx 的截止时间试。
func (s *Sender) finish(ctx context.Context, pending batch) {
	if !pending.empty() {
		if err := s.deliver(ctx, pending); err != nil {
			s.logger.Warn("服务停止时发送调用记录超时，放弃这一批",
				"records", len(pending.records), "billing", len(pending.billing), "error", err)
		}
	}
	for {
		next := s.take(s.batchSize)
		if next.empty() {
			return
		}
		if err := s.deliver(ctx, next); err != nil {
			s.logger.Warn("服务停止时发送调用记录超时，放弃剩余",
				"records", len(next.records)+s.size(), "billing", len(next.billing)+s.billingSize(), "error", err)
			return
		}
	}
}

// deliver 把一批送到端点，可重试的错误按退避重试到送出或 ctx 结束。
//
// 返回 nil 表示这批已经处理完（送出，或遇到重试也不会变好的响应）；返回错误表示
// ctx 结束了，这批还没送出，由调用方决定留着还是放弃。
func (s *Sender) deliver(ctx context.Context, b batch) error {
	backoff := s.initialDelay
	attempt := 0
	for {
		err := s.post(ctx, b)
		switch {
		case err == nil:
			return nil
		case errors.Is(err, errNotRetryable):
			s.logger.Warn("摄入端点拒绝这批记录，不再重试",
				"records", len(b.records), "billing", len(b.billing), "error", err)
			return nil
		case ctx.Err() != nil:
			return err
		}
		attempt++
		s.logger.Warn("发送调用记录失败，将重试",
			"attempt", attempt, "records", len(b.records), "billing", len(b.billing), "error", err, "backoff", backoff)
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return err
		}
		backoff *= 2
		if backoff > s.maxDelay {
			backoff = s.maxDelay
		}
	}
}

// post 发一批。4xx（429 除外）是重试也不会变好的响应，单独标出来。
//
// 调用记录与计费行同一批 POST：web 那边的两张表独立插入，靠 request_id 关联，
// 不要求有序，因此不必分成两次请求。
func (s *Sender) post(ctx context.Context, b batch) error {
	body, err := json.Marshal(struct {
		Records []Row         `json:"records"`
		Billing []billing.Row `json:"billing,omitempty"`
	}{Records: b.records, Billing: b.billing})
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("x-callback-secret", s.secret)

	response, err := s.client.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	// 读掉响应体好让连接复用；内容只用来看状态码，不解析。
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return nil
	}
	err = fmt.Errorf("摄入端点返回状态码 %d", response.StatusCode)
	if response.StatusCode >= 400 && response.StatusCode < 500 && response.StatusCode != http.StatusTooManyRequests {
		return fmt.Errorf("%w: %v", errNotRetryable, err)
	}
	return err
}

// batch 是取出来、准备发送的一批。两类行一起带：web 两者独立插入，不要求有序。
type batch struct {
	records []Row
	billing []billing.Row
}

// empty 报告这批没有东西可发。
func (b batch) empty() bool {
	return len(b.records) == 0 && len(b.billing) == 0
}

// take 从两个队首各取最多 n 行。
func (s *Sender) take(n int) batch {
	s.mu.Lock()
	defer s.mu.Unlock()
	return batch{
		records: takeFirst(&s.queue, n),
		billing: takeFirst(&s.billing, n),
	}
}

// takeFirst 从队首取最多 n 个，返回取到的副本。
func takeFirst[T any](queue *[]T, n int) []T {
	if len(*queue) == 0 {
		return nil
	}
	if n > len(*queue) {
		n = len(*queue)
	}
	batch := append([]T(nil), (*queue)[:n]...)
	*queue = (*queue)[n:]
	return batch
}

// size 返回调用记录队列当前行数。
func (s *Sender) size() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.queue)
}

// billingSize 返回计费队列当前行数。
func (s *Sender) billingSize() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.billing)
}

// signal 叫醒后台循环，已经有信号在排队时不重复发。
func (s *Sender) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Dropped 返回因为队列满而丢掉的调用记录行数。给测试与观测用。
func (s *Sender) Dropped() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dropped
}

// DroppedBilling 返回因为队列满而丢掉的计费行数。
func (s *Sender) DroppedBilling() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.droppedBilling
}

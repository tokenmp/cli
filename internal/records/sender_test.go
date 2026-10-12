package records

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	coreconfig "github.com/tokenmp/core/config"

	"github.com/tokenmp/cli/internal/billing"
)

// receivedBatch 是摄入端点收到的一批，测试用它核对内容与请求头。
type receivedBatch struct {
	secret string
	ids    []string
}

// collectingServer 收下每一批并转发到 channel，方便测试等它到。
func collectingServer(t *testing.T) (*httptest.Server, <-chan receivedBatch) {
	t.Helper()

	batches := make(chan receivedBatch, 64)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var payload struct {
			Records []Row `json:"records"`
		}
		_ = json.Unmarshal(body, &payload)
		ids := make([]string, 0, len(payload.Records))
		for _, row := range payload.Records {
			ids = append(ids, row.RequestID)
		}
		batches <- receivedBatch{secret: r.Header.Get("x-callback-secret"), ids: ids}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"ok":true}`)
	}))
	t.Cleanup(server.Close)
	return server, batches
}

// newTestSender 造一个发送间隔很长的发送端：只有攒够一批或停机冲刷才会发出去。
func newTestSender(endpoint string, batchSize int) *Sender {
	cfg := Config{Endpoint: endpoint, BatchSize: batchSize, FlushInterval: coreconfig.Duration(time.Hour)}
	cfg.Defaults()
	sender := New(cfg, "test-secret", slog.New(slog.DiscardHandler))
	sender.initialDelay = 5 * time.Millisecond
	sender.maxDelay = 10 * time.Millisecond
	return sender
}

// waitBatch 等一批到达，超时算失败。
func waitBatch(t *testing.T, batches <-chan receivedBatch) receivedBatch {
	t.Helper()
	select {
	case batch := <-batches:
		return batch
	case <-time.After(5 * time.Second):
		t.Fatal("等摄入端点收到请求超时")
		return receivedBatch{}
	}
}

// TestSenderPostsBatch 一次发一整批，带上共享密钥请求头。
func TestSenderPostsBatch(t *testing.T) {
	server, batches := collectingServer(t)
	sender := newTestSender(server.URL, 2)
	defer sender.Close(time.Second)

	sender.Add(Row{RequestID: "req_1"})
	sender.Add(Row{RequestID: "req_2"})

	batch := waitBatch(t, batches)
	if batch.secret != "test-secret" {
		t.Errorf("x-callback-secret 是 %q，期望 test-secret", batch.secret)
	}
	if len(batch.ids) != 2 || batch.ids[0] != "req_1" || batch.ids[1] != "req_2" {
		t.Errorf("第一批的内容不对: %v", batch.ids)
	}
}

// TestSenderRetriesWholeBatch 守住重试语义：失败重发整批，不在本地去重，重复的
// request_id 由对端按幂等键处理。
func TestSenderRetriesWholeBatch(t *testing.T) {
	var (
		mu       sync.Mutex
		attempts int
		seen     [][]string
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var payload struct {
			Records []Row `json:"records"`
		}
		_ = json.Unmarshal(body, &payload)
		ids := make([]string, 0, len(payload.Records))
		for _, row := range payload.Records {
			ids = append(ids, row.RequestID)
		}
		mu.Lock()
		attempts++
		current := attempts
		seen = append(seen, ids)
		mu.Unlock()
		if current < 3 {
			http.Error(w, "上游暂时不可用", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	sender := newTestSender(server.URL, 1)
	defer sender.Close(time.Second)
	sender.Add(Row{RequestID: "req_retry"})

	// 等到第三次之后成功，覆盖率重试。
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		done := attempts >= 3
		mu.Unlock()
		if done {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("等重试超时，只发了 %d 次", attempts)
		}
		time.Sleep(5 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	for i, ids := range seen {
		if len(ids) != 1 || ids[0] != "req_retry" {
			t.Errorf("第 %d 次发的内容是 %v，期望重复发同一批 [req_retry]", i+1, ids)
		}
	}
}

// TestSenderDropsOldestWhenQueueFull 队列满时丢最旧的一条，留下的是最近发生的。
func TestSenderDropsOldestWhenQueueFull(t *testing.T) {
	server, batches := collectingServer(t)
	sender := newTestSender(server.URL, 100)
	sender.queueSize = 2
	defer sender.Close(time.Second)

	for _, id := range []string{"req_0", "req_1", "req_2", "req_3", "req_4"} {
		sender.Add(Row{RequestID: id})
	}
	if got := sender.Dropped(); got != 3 {
		t.Errorf("丢掉的行数是 %d，期望 3", got)
	}

	// 停机冲刷把留下的两行发出去。
	sender.Close(time.Second)
	batch := waitBatch(t, batches)
	if len(batch.ids) != 2 || batch.ids[0] != "req_3" || batch.ids[1] != "req_4" {
		t.Errorf("队列里留下的是 %v，期望 [req_3 req_4]", batch.ids)
	}
}

// TestSenderShutdownFlushes 停机时把攒着不够一批的行发完。
func TestSenderShutdownFlushes(t *testing.T) {
	server, batches := collectingServer(t)
	sender := newTestSender(server.URL, 100)

	sender.Add(Row{RequestID: "req_a"})
	sender.Add(Row{RequestID: "req_b"})
	sender.Close(time.Second)

	batch := waitBatch(t, batches)
	if len(batch.ids) != 2 || batch.ids[0] != "req_a" || batch.ids[1] != "req_b" {
		t.Errorf("停机冲刷发的是 %v，期望 [req_a req_b]", batch.ids)
	}
}

// TestSenderAddDoesNotBlockWhenEndpointDown 端点不通时 Add 仍然立刻返回：记账慢不
// 该让转发路径排队。发送与重试都在后台 goroutine 上。
func TestSenderAddDoesNotBlockWhenEndpointDown(t *testing.T) {
	sender := newTestSender("http://127.0.0.1:1/api/internal/records", 1)
	defer sender.Close(50 * time.Millisecond)

	start := time.Now()
	for i := 0; i < 200; i++ {
		sender.Add(Row{RequestID: fmt.Sprintf("req_%d", i)})
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("往不通的端点加 200 行花了 %s，Add 不该阻塞", elapsed)
	}
}

// TestSenderPostsRecordsAndBillingTogether 调用记录与计费行同一批 POST：web 两张表
// 独立插入，靠 request_id 关联。
func TestSenderPostsRecordsAndBillingTogether(t *testing.T) {
	type payload struct {
		Records []Row         `json:"records"`
		Billing []billing.Row `json:"billing"`
	}
	received := make(chan payload, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var got payload
		_ = json.Unmarshal(body, &got)
		received <- got
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	sender := newTestSender(server.URL, 100)
	sender.Add(Row{RequestID: "req_1"})
	sender.AddBilling(billing.Row{RequestID: "req_1", RevenueAmount: 0.5, Status: "settled"})
	sender.Close(time.Second)

	select {
	case got := <-received:
		if len(got.Records) != 1 || got.Records[0].RequestID != "req_1" {
			t.Errorf("records 是 %+v", got.Records)
		}
		if len(got.Billing) != 1 || got.Billing[0].RequestID != "req_1" {
			t.Errorf("billing 是 %+v", got.Billing)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("等摄入端点收到请求超时")
	}
}

// Package code 提供验证码内存存储（进程内 Map 语义）。
//
// 特性：进程内存储、TTL 过期、单码尝试次数上限、用后即删、60s 定时清理。
// 多实例部署需换共享存储（当前单实例内存语义，接口化便于将来换 Redis）。
package code

import (
	"crypto/rand"
	"math/big"
	"sync"
	"time"
)

// Entry 是验证码条目。
type Entry struct {
	UserID    string
	Email     string
	ExpiresAt time.Time
	Attempts  int
	// Need2FA 仅设备登录码使用（deviceLoginCodes 条目携带）。
	Need2FA bool
}

// Store 是验证码内存存储。key 为 6 位数字验证码。
type Store struct {
	mu    sync.Mutex
	items map[string]Entry
	ttl   time.Duration
	done  chan struct{}
}

// NewStore 创建验证码存储，并启动 60s 定时清理过期条目的 goroutine。
// 用 Store.Stop 停止清理（测试用）。
func NewStore(ttl time.Duration) *Store {
	s := &Store{
		items: make(map[string]Entry),
		ttl:   ttl,
		done:  make(chan struct{}),
	}
	go s.cleanupLoop()
	return s
}

// cleanupLoop 每 60s 清理过期条目。
func (s *Store) cleanupLoop() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-ticker.C:
			now := time.Now()
			s.mu.Lock()
			for k, e := range s.items {
				if now.After(e.ExpiresAt) {
					delete(s.items, k)
				}
			}
			s.mu.Unlock()
		}
	}
}

// Stop 停止后台清理（测试回收）。
func (s *Store) Stop() {
	select {
	case <-s.done:
	default:
		close(s.done)
	}
}

// Set 存入验证码。若 entry.ExpiresAt 为零值则自动设为 now + ttl
// （调用方已显式设置时保留原值——更新尝试次数不应延长 TTL）。
func (s *Store) Set(code string, e Entry) {
	if e.ExpiresAt.IsZero() {
		e.ExpiresAt = time.Now().Add(s.ttl)
	}
	s.mu.Lock()
	s.items[code] = e
	s.mu.Unlock()
}

// SetFresh 存入全新验证码条目：该 code 已存在未过期条目时拒绝（返回 false），
// 保证任一时刻一个 code 只属于一个用户，杜绝跨用户静默覆盖
// （6 位码是 900k 的共享命名空间，A 用户签发时若覆盖了 B 用户的同码条目，
// B 已知的 code 就会被解析到 A 的账号——设备登录码链可被据此接管）。
// 尝试计数等原地写回（同一 entry）仍用 Set。
func (s *Store) SetFresh(code string, e Entry) bool {
	if e.ExpiresAt.IsZero() {
		e.ExpiresAt = time.Now().Add(s.ttl)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if cur, ok := s.items[code]; ok && time.Now().Before(cur.ExpiresAt) {
		return false
	}
	s.items[code] = e
	return true
}

// Mint 生成并存入一个与现有未过期条目不冲突的验证码并返回之。
// 900k 键空间下冲突概率可忽略；连续冲突视为异常，最后退回 Set 保证可用。
func (s *Store) Mint(e Entry) string {
	for i := 0; i < 100; i++ {
		c := GenerateCode()
		if s.SetFresh(c, e) {
			return c
		}
	}
	c := GenerateCode()
	s.Set(c, e)
	return c
}

// Get 取出条目（过期判定由调用方负责，Get 不主动判过期——固定契约，勿改）。
func (s *Store) Get(code string) (Entry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.items[code]
	return e, ok
}

// Delete 删除验证码（一次性消费）。
func (s *Store) Delete(code string) {
	s.mu.Lock()
	delete(s.items, code)
	s.mu.Unlock()
}

// GenerateCode 生成 6 位数字验证码（对齐 crypto.randomInt(100000,1000000)）。
func GenerateCode() string {
	n, err := rand.Int(rand.Reader, big.NewInt(900000))
	if err != nil {
		// 极低概率随机源失败：回退时间戳微秒派生，避免 panic。
		return time.Now().Format("150405.000")[6:9] + "000"
	}
	return n.Add(n, big.NewInt(100000)).String()
}

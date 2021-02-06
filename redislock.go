package redislock

import (
	"context"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/rs/xid"
)

var (
	lockRefresh = redis.NewScript(`
			if redis.call("pttl", KEYS[1]) > 0 and redis.call("get", KEYS[1]) == ARGV[1] then
				return redis.call("pexpire", KEYS[1], ARGV[2])
			else
				return 0
			end
	`)
	lockUnlock = redis.NewScript(`
			if redis.call("get", KEYS[1]) == ARGV[1] then
				if KEYS[2] ~= "" then
					redis.call("del", KEYS[2])
					redis.call("lpush", KEYS[2], 1)
					redis.call("expire", KEYS[2], ARGV[2])
				end
				return redis.call("del", KEYS[1])
			else
				return 0
			end
	`)
)

// 锁公共项: Redis 连接, context, 锁名, 阻塞键名, 阻塞超时时间
type RedisLocker struct {
	rdb  *redis.Client
	ctx  context.Context
	key  string
	keyB string
	ttlB time.Duration
}

// 锁标识: 锁环境, 锁随机值, 锁生命周期
type RedisLock struct {
	locker *RedisLocker
	value  string
	ttl    time.Duration
}

// 新建锁环境, 默认非阻塞, 使用 context.Background()
func New(rdb *redis.Client, key string) *RedisLocker {
	return CTXNew(rdb, context.Background(), key, "")
}

// 新建锁环境, 默认阻塞, 使用 context.Background()
func NewBlocking(rdb *redis.Client, key, keyB string) *RedisLocker {
	return CTXNew(rdb, context.Background(), key, keyB)
}

// 新建锁环境
func CTXNew(rdb *redis.Client, ctx context.Context, key string, keyB string) *RedisLocker {
	return &RedisLocker{rdb: rdb, ctx: ctx, key: key, keyB: keyB}
}

// 新建空锁
func (c *RedisLocker) New(ttl time.Duration) *RedisLock {
	return &RedisLock{c, "", ttl}
}

// 获取锁
func (c *RedisLocker) Lock(ttl time.Duration) (*RedisLock, bool) {
	return c.TryLock(ttl, 0, 0)
}

// 获取锁 (安全模式, 重试 2 次)
func (c *RedisLocker) SafeLock(ttl time.Duration) (*RedisLock, bool) {
	return c.TryLock(ttl, 2, time.Microsecond)
}

// 多次尝试获取锁
func (c *RedisLocker) TryLock(ttl time.Duration, retry int, retryDelay time.Duration) (*RedisLock, bool) {
	if ttl < time.Millisecond {
		return c.New(ttl), false
	}

	if c.keyB != "" {
		// 阻塞模式, BLPop 最小超时为 1 秒
		if ttl < time.Second {
			c.ttlB = time.Second
		} else {
			c.ttlB = ttl
		}
	}

	value := xid.New().String()
	for i := 0; i <= retry; i++ {
		// SET key value EX|PX 10 NX
		if c.rdb.SetNX(c.ctx, c.key, value, ttl).Val() {
			if c.keyB != "" {
				c.rdb.Del(c.ctx, c.keyB)
			}
			return &RedisLock{c, value, ttl}, true
		}
		if retryDelay > 0 {
			time.Sleep(retryDelay)
		}
	}

	if c.keyB != "" {
		if v := c.rdb.BLPop(c.ctx, c.ttlB, c.keyB).Val(); len(v) > 1 && v[1] == "1" {
			// 获取到数据时尝试取锁
			return c.TryLock(ttl, retry, retryDelay)
		}
	}

	return c.New(ttl), false
}

// 获取锁(名)当前生命周期(毫秒)
func (c *RedisLocker) TTL() time.Duration {
	return c.rdb.PTTL(c.ctx, c.key).Val()
}

// 重新获取锁
func (l *RedisLock) Lock() bool {
	return l.TryLock(0, 0)
}

// 重新获取锁 (安全模式, 重试 2 次)
func (l *RedisLock) SafeLock() bool {
	return l.TryLock(2, time.Microsecond)
}

// 重新多次尝试获取锁 (指定重试时间间隔)
func (l *RedisLock) TryLock(retry int, retryDelay time.Duration) bool {
	if newLock, ok := l.locker.TryLock(l.ttl, retry, retryDelay); ok {
		l.value = newLock.value
		return true
	}

	return false
}

// 释放锁
func (l *RedisLock) Unlock() bool {
	if l.value != "" {
		ok, _ := lockUnlock.Run(l.locker.ctx, l.locker.rdb,
			[]string{l.locker.key, l.locker.keyB}, l.value, int64(l.locker.ttlB/time.Second)).Bool()
		if ok {
			l.value = ""
			return true
		}
	}
	return false
}

// 保活锁
func (l *RedisLock) Keepalive() bool {
	return l.Refresh(l.ttl)
}

// 重置锁当前生命周期 (禁止使用负数)
func (l *RedisLock) Refresh(ttl time.Duration) bool {
	if l.value != "" && ttl > 0 {
		ok, _ := lockRefresh.Run(l.locker.ctx, l.locker.rdb, []string{l.locker.key},
			l.value, int64(ttl/time.Millisecond)).Bool()
		return ok
	}
	return false
}

// 获取锁(名)当前生命周期(毫秒)
func (l *RedisLock) TTL() time.Duration {
	return l.locker.TTL()
}

// 设置锁默认生命周期 (下次取锁成功或保活时生效)
func (l *RedisLock) SetTTL(ttl time.Duration) *RedisLock {
	l.ttl = ttl
	return l
}

// 锁键名
func (l *RedisLock) Key() string {
	return l.locker.key
}

// 锁键值
func (l *RedisLock) Value() string {
	return l.value
}

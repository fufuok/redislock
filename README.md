# Go-RedisLock

基于 `Redis` 的分布式锁, 适用于多 `Worker` 或多服务器共用 `Redis` (单机/集群)服务的场景.

## 特征

- **可靠**: 基于 `Redis` 原子操作.

- **易用**: 取个锁名, 给定锁过期时间就有了一个业务锁. 可以有无限个互不干扰的锁.

- **获取锁**: 

  - `Lock()` 常用, 取一次锁.

  - `SafeLock()` 第一次未获得锁时, 每 `1us` 重试 `1` 次, 共重试 `2` 次. 

    非主动释放锁的情景下(即依靠锁过期时间自动释放锁的场景)取锁时推荐使用.

  - `TryLock()` 指定重试次数, 指定重试时间间隔, 直到取锁成功或重试结束.

- **保活锁**: 以锁当前的设定重置其生命周期.

- **刷新锁**: 给锁设定一个新的生命周期.

- **释放锁**: 锁释放后可立即被重新获取.

## 安装

```shell
go get github.com/fufuok/redislock
```

## 获取锁的几种方式

```go
// 0.1 指定锁名 -> 初始化锁环境对象(RedisLocker) -> 指定锁生命周期 -> 获取锁(RedisLock)
locker1 := redislock.New(rdb, "lock1")
lock1, ok := locker1.Lock(1*time.Second)
lock1.Unlock()

// 0.2 获取过锁对象后, 可以直接使用锁对象获取锁
// 此时, 锁名和锁生命周期均为初始化时的值: lock1, 1*time.Second
ok = lock1.Lock()
lock1.Unlock()

// 0.3 也可以先初始化个空锁, 然后使用锁对象获取锁
lock2 := redislock.New(rdb, "lock2").New(1*time.Second)
ok = lock2.Lock()
lock2.Unlock()

// 0.4 锁环境对象(RedisLocker)和锁对象(RedisLock)都有 3 个获取锁的方法, 效果相同
// Lock() 获取一次
// SafeLock() 获取锁时重试 2 次, 每次间隔 1us, 常用于锁过期后获取锁的场景
// TryLock() 指定获取锁时重试次数和每次重试的时间间隔
locker1.Lock(1*time.Second)
locker1.SafeLock(1*time.Second)
locker1.TryLock(1*time.Second, 2, 3*time.Millisecond)
// 锁对象获取锁时默认使用锁环境初始化时的锁生命周期
lock1.Lock()
lock1.SafeLock()
lock1.TryLock(2, 3*time.Millisecond)
// 若要更换锁生命周期, 可以使用 Refresh() 方法 (立即生效)
lock1.Refresh(5*time.Second)
// 若要变更锁的默认生命周期 (下次取锁时生效)
lock1.SetTTL(8*time.Second).Lock()
```

## 示例

```go
// example/main.go
package main

import (
	"fmt"
	"time"

	"github.com/fufuok/redislock"
	"github.com/go-redis/redis/v8"
)

func main() {
	// Redis 连接 (可与项目共用, 确保连接正确)
	rdb := redis.NewClient(&redis.Options{
		Network: "tcp",
		Addr:    "127.0.0.1:6379",
	})

	defer func() {
		_ = rdb.Close()
	}()

	// 1. 简单使用
	// 传入 Redis 连接和锁名 (锁名称是区分锁的唯一标识)
	// 获取锁时传入锁的过期时间 (生命周期)
	lock, ok := redislock.New(rdb, "simpleLock").Lock(10 * time.Millisecond)
	if ok {
		fmt.Printf("取锁成功.\n  锁名: %s\n  锁生命周期: %s\n  锁对象: %+v\n", lock.Key(), lock.TTL(), lock)
	} else {
		fmt.Printf("失败? 锁对象: %+v\n", lock)
	}

	// 刷新锁生命周期 (临时更新锁的生命周期)
	ok = lock.Refresh(1 * time.Hour)
	fmt.Printf("刷新锁: 成功 == %v, 锁生命周期 > 59m: %s\n", ok, lock.TTL())

	// 保活锁: 重置锁生命周期 (重置为获取锁时设定的生命周期)
	ok = lock.Keepalive()
	fmt.Printf("保活锁: 成功 == %v, 锁生命周期 >=8ms <=10ms: %s\n", ok, lock.TTL())

	// 锁占用期, 无法被再次获取到
	ok = lock.Lock()
	if ok {
		fmt.Printf("异常? 锁对象: %+v\n", lock)
	} else {
		fmt.Printf("锁被占用, 无法获取, 锁生命周期: %s\n", lock.TTL())
	}

    // 锁占用期, 新建锁对象(锁名相同时)也无法获取到 (其他协程或新建锁对象)
	newLock, ok := redislock.New(rdb, "simpleLock").Lock(10 * time.Millisecond)
	if ok {
		fmt.Printf("异常? 锁对象: %+v\n", newLock)
	} else {
		// 锁生命周期以锁名为标识, 即返回的是 Redis 键名 TTL
		fmt.Printf("锁被占用, 无法获取, 锁生命周期: %s\n", newLock.TTL())
	}

	// 锁过期后可重新获取到锁, 此时建议使用 SafeLock()
	time.Sleep(lock.TTL())
	ok = lock.SafeLock()
	if ok {
		fmt.Printf("锁过期, 重新取锁成功.\n  锁名: %s\n  锁生命周期: %s\n  锁对象: %+v\n", lock.Key(), lock.TTL(), lock)
	} else {
		fmt.Printf("失败? 锁对象: %+v\n", lock)
	}

	// 主动释放锁后可立即被重新获取
	ok = lock.Unlock()
	if ok {
		fmt.Printf("主动释放锁: 成功 == %v, 锁生命周期: %s, 锁对象: %+v\n", ok, lock.TTL(), lock)
		fmt.Printf("重新获取锁: 成功 == %v, 锁生命周期: %s, 锁对象: %+v\n", lock.Lock(), lock.TTL(), lock)
	} else {
		fmt.Printf("失败? 锁对象: %+v\n", lock)
	}

	// 按指定时间间隔重试取锁
	ok = lock.TryLock(3, 5*time.Millisecond)
	if ok {
		fmt.Printf("重试 3 次(每次间隔 5ms), > 锁生命周期 10ms, 取锁成功 == %v\n", ok)
	} else {
		fmt.Printf("失败? 锁生命周期: %s, 锁对象: %+v\n", lock.TTL(), lock)
	}

	// 设置非法锁生命周期 (禁止使用负数)
	errLock, ok := redislock.New(rdb, "TestErrTTL").Lock(-1 * time.Second)
	if ok {
		fmt.Printf("异常? 锁生命周期: %s, 锁对象: %+v\n", errLock.TTL(), errLock)
	} else {
		fmt.Printf("非法生命周期值无法获得锁: %+v\n", errLock)
		ok = errLock.SetTTL(2 * time.Second).Lock()
		fmt.Printf("  SetTTL() 重设锁后获取成功: %v, %+v\n", ok, errLock)
	}

	// 2. 通常使用场景
	// commonLock()

	// 3. 模拟定时任务场景
	// timeoutLock()

	// 4. 模拟必须任务执行完成才让出锁的场景
	// keepaliveLock()

	fmt.Println("the end.")
}
```





*ff*
package redislock

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	. "github.com/smartystreets/goconvey/convey"
)

var (
	rdb               *redis.Client
	ctx               = context.Background()
	myLocker          *RedisLocker
	myBlockingLocker  *RedisLocker
	myLockKey         = "testRedisLock"
	myLockKeyBlocking = "testRedisLockBlocking"
	myLockTTL         = 10 * time.Millisecond
)

func TestRedisLock_Blocking(t *testing.T) {
	Convey("阻塞取锁测试", t, func() {
		So(setup(), ShouldBeTrue)

		// 初始化锁环境
		myBlockingLocker = NewBlocking(rdb, myLockKey, myLockKeyBlocking)

		Convey("30 协程同时抢锁, 每个协程都能抢到 1 次", func() {
			n := 0
			wg := &sync.WaitGroup{}

			for i := 1; i <= 20; i++ {
				wg.Add(1)

				// 锁生命周期小于 1 秒, 阻塞模式工作正常
				myLock := myBlockingLocker.New(20 * time.Millisecond)

				go func(i int) {
					defer wg.Done()

					ok := myLock.Lock()
					if ok {
						n += i
						time.Sleep(5 * time.Millisecond)
						myLock.Unlock()
					}
				}(i)
			}

			for i := 21; i <= 30; i++ {
				wg.Add(1)
				myLock := myBlockingLocker.New(1 * time.Second)

				go func(i int) {
					defer wg.Done()

					ok := myLock.Lock()
					if ok {
						n += i
						time.Sleep(5 * time.Millisecond)
						myLock.Unlock()
					}
				}(i)
			}

			wg.Wait()

			So(n, ShouldEqual, 465)
		})

		Reset(func() {
			teardown()
		})
	})
}

func TestRedisLock(t *testing.T) {
	Convey("初始化锁(默认锁生命周期: 10ms)", t, func() {
		So(setup(), ShouldBeTrue)

		// 初始化锁环境
		myLocker = New(rdb, myLockKey)

		Convey("锁占用期内无法被再次获取", func() {
			myLock, ok := myLocker.Lock(myLockTTL * 10)
			defer myLock.Unlock()

			So(ok, ShouldBeTrue)

			Convey("Lock 尝试取锁不会成功", func() {
				So(myLock.Lock(), ShouldBeFalse)
			})

			Convey("SafeLock 尝试取锁不会成功", func() {
				So(myLock.SafeLock(), ShouldBeFalse)
			})

			Convey("TryLock 尝试取锁不会成功", func() {
				So(myLock.TryLock(1, 1*time.Millisecond), ShouldBeFalse)
			})
		})

		Convey("锁过期后成功获取锁", func() {
			myLock, ok := myLocker.Lock(myLockTTL)
			defer myLock.Unlock()

			So(ok, ShouldBeTrue)

			lockValue1 := myLock.Value()
			for i := 0; i < 20; i++ {
				Convey(fmt.Sprintf("20 次测试 (%d)", i), func() {
					time.Sleep(myLockTTL)
					So(myLock.SafeLock(), ShouldBeTrue)
					So(myLock.Value(), ShouldNotEqual, lockValue1)
				})
			}
		})

		Convey("重试方式获取锁", func() {
			myLock, ok := myLocker.Lock(myLockTTL)
			defer myLock.Unlock()

			So(ok, ShouldBeTrue)

			lockValue2 := myLock.Value()
			Convey("间隔 4ms 重试 3 次 (达到锁过期时间, 将获取到锁)", func() {
				So(myLock.TryLock(3, 4*time.Millisecond), ShouldBeTrue)
				So(myLock.Value(), ShouldNotEqual, lockValue2)
			})
		})

		Convey("锁过期时间验证", func() {
			myLock, ok := myLocker.Lock(myLockTTL)
			defer myLock.Unlock()

			So(ok, ShouldBeTrue)
			So(myLock.TTL(), ShouldBeLessThanOrEqualTo, myLockTTL)
			So(myLock.TTL(), ShouldBeGreaterThanOrEqualTo, myLockTTL-5*time.Millisecond)
		})

		Convey("保活锁和刷新锁生命周期", func() {
			myLock, ok := myLocker.Lock(myLockTTL)
			defer myLock.Unlock()

			So(ok, ShouldBeTrue)

			Convey("保活锁: 延时 5ms 后 -> 恢复生命周期(10ms) -> 生命周期 >= 6ms", func() {
				time.Sleep(5 * time.Millisecond)

				So(myLock.Keepalive(), ShouldBeTrue)
				So(myLock.TTL(), ShouldBeGreaterThanOrEqualTo, 6*time.Millisecond)
			})

			Convey("刷新锁生命周期为 1m -> 锁生命周期 > 59s", func() {
				So(myLock.Refresh(1*time.Minute), ShouldBeTrue)
				So(myLock.TTL(), ShouldBeGreaterThanOrEqualTo, 59*time.Second)
			})

			Convey("设置锁参数生命周期为 2m -> 保活锁 -> 锁生命周期 > 119s", func() {
				So(myLock.SetTTL(2*time.Minute).Keepalive(), ShouldBeTrue)
				So(myLock.TTL(), ShouldBeGreaterThanOrEqualTo, 119*time.Second)
			})

		})

		Convey("已过期或已释放的锁无法保活", func() {
			myLock, ok := myLocker.Lock(myLockTTL)
			defer myLock.Unlock()

			So(ok, ShouldBeTrue)

			Convey("延时 10ms 后将保活失败, 锁生命周期: -3 或 0", func() {
				// 多延时 1ms 确保过期
				time.Sleep(11 * time.Millisecond)

				So(myLock.Keepalive(), ShouldBeFalse)
				So(myLock.TTL(), ShouldBeLessThanOrEqualTo, 0)
				So(myLock.SafeLock(), ShouldBeTrue)
				So(myLock.Unlock(), ShouldBeTrue)
			})

			Convey("主动释放锁后将保活失败, 锁生命周期: -3 或 0", func() {
				So(myLock.Unlock(), ShouldBeTrue)
				So(myLock.Keepalive(), ShouldBeFalse)
				So(myLock.TTL(), ShouldBeLessThanOrEqualTo, 0)
			})
		})

		Convey("主动释放锁后将可以立即重新获取到锁", func() {
			myLock := myLocker.New(1 * time.Second)

			for i := 0; i < 10; i++ {
				Convey(fmt.Sprintf("40 次测试 (%d)", i), func() {
					// myLock.Lock()
					So(myLock.Lock(), ShouldBeTrue)
					So(myLock.Unlock(), ShouldBeTrue)

					// myLocker.Lock()
					newLock, ok := myLocker.Lock(myLockTTL)
					So(ok, ShouldBeTrue)
					So(newLock.Unlock(), ShouldBeTrue)

					// New().Lock()
					NewLock, ok := New(rdb, myLockKey).Lock(myLockTTL)
					So(ok, ShouldBeTrue)
					So(NewLock.Unlock(), ShouldBeTrue)

					// New().SafeLock()
					saveLock, ok := New(rdb, myLockKey).SafeLock(myLockTTL)
					So(ok, ShouldBeTrue)
					So(saveLock.Unlock(), ShouldBeTrue)
				})
			}
		})

		Convey("设置非法的锁生命周期", func() {
			myLock, ok := myLocker.Lock(999 * time.Microsecond)
			So(ok, ShouldBeFalse)

			// 非法锁, 无法刷新生命周期
			So(myLock.Refresh(1*time.Second), ShouldBeFalse)

			ok = myLock.SetTTL(1 * time.Second).Lock()
			So(ok, ShouldBeTrue)

			// 正确锁, 刷新生命周期值非法时无效, 不产生影响
			So(myLock.Refresh(-1*time.Millisecond), ShouldBeFalse)

			myLock.Unlock()
		})

		Convey("锁键值获取", func() {
			myLock, _ := myLocker.Lock(1 * time.Second)
			defer myLock.Unlock()
			So(myLock.Key(), ShouldEqual, myLockKey)
			So(myLock.Value(), ShouldNotBeBlank)
		})

		Convey("抢锁测试(锁生命周期: 1m)", func() {

			Convey("20 协程同时抢锁 (每协程重试 20 次), 只有 1 个能抢到", func() {
				var n uint64
				wg := &sync.WaitGroup{}

				for i := 1; i <= 20; i++ {
					wg.Add(1)

					go func(i int) {
						defer wg.Done()

						myLock, ok := myLocker.TryLock(1*time.Second, 20, 1*time.Millisecond)
						if ok {
							atomic.AddUint64(&n, 1)
							time.Sleep(100 * time.Millisecond)
							myLock.Unlock()
						}
					}(i)
				}

				wg.Wait()

				So(n, ShouldEqual, 1)
			})
		})

		Reset(func() {
			teardown()
		})
	})
}

func setup() bool {
	rdb = redis.NewClient(&redis.Options{
		Network: "tcp",
		Addr:    "127.0.0.1:6379",
	})

	return rdb.Ping(ctx).Val() == "PONG"
}

func teardown() {
	_ = rdb.Close()
}

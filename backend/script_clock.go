package main

import (
	"sync"
	"time"
)

// scriptClock 是一次脚本运行的时间预算：预算耗尽即超时（onExpire 由调用方接好，
// 通常是取消这次运行的 ctx）。脚本等用户输入（nd.input）时时钟暂停——用户想多久
// 都不占脚本的预算；parent 非空时（composer 调起的条目脚本）暂停会顺着链传上去，
// 整个运行都不会因为有人在嵌套脚本里输入而超时。
type scriptClock struct {
	mu       sync.Mutex
	parent   *scriptClock
	onExpire func()
	timer    *time.Timer
	total    time.Duration
	// remaining 是暂停时结存的预算；started 是当前这段计时的起点。
	remaining time.Duration
	started   time.Time
	pauses    int
	expired   bool
}

// newScriptClock 起一份 budget 的预算，立即开始计时；预算耗尽时调一次 onExpire。
func newScriptClock(budget time.Duration, parent *scriptClock, onExpire func()) *scriptClock {
	clock := &scriptClock{
		parent:    parent,
		onExpire:  onExpire,
		total:     budget,
		remaining: budget,
		started:   time.Now(),
	}
	clock.timer = time.AfterFunc(budget, clock.expire)
	return clock
}

// expire 是计时器到点的回调：只在走时状态下才算超时（暂停中到点说明 Pause 与
// 计时器到期撞在了一起，预算结存已归 remaining，Resume 时会立即重新到点）。
func (c *scriptClock) expire() {
	c.mu.Lock()
	if c.expired || c.pauses > 0 {
		c.mu.Unlock()
		return
	}
	c.expired = true
	onExpire := c.onExpire
	c.mu.Unlock()
	if onExpire != nil {
		onExpire()
	}
}

// TimedOut 报告这份时钟自己的预算是否已经耗尽。不看 parent 链：外层运行到点
// 会取消 ctx，那次结束按「已取消」报告（与旧行为一致），超时只认自己的预算。
func (c *scriptClock) TimedOut() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.expired
}

// budget 是这份时钟的总预算，超时消息里报的就是它。
func (c *scriptClock) budget() time.Duration {
	return c.total
}

// Pause 停住时钟（开始等用户输入）。可嵌套：Resume 次数与 Pause 对上才重新走。
// 链上的 parent 一起暂停。
func (c *scriptClock) Pause() {
	c.mu.Lock()
	c.pauses++
	if c.pauses == 1 && !c.expired {
		c.timer.Stop()
		c.remaining -= time.Since(c.started)
		if c.remaining < 0 {
			c.remaining = 0
		}
	}
	parent := c.parent
	c.mu.Unlock()
	if parent != nil {
		parent.Pause()
	}
}

// Resume 重新走时，与 Pause 配对；预算已在暂停前耗尽时立即到点。
func (c *scriptClock) Resume() {
	c.mu.Lock()
	if c.pauses > 0 {
		c.pauses--
		if c.pauses == 0 && !c.expired {
			c.started = time.Now()
			c.timer.Reset(c.remaining)
		}
	}
	parent := c.parent
	c.mu.Unlock()
	if parent != nil {
		parent.Resume()
	}
}

// stop 结束时钟（运行结束）：之后不再触发超时，Pause/Resume 照常安全。
func (c *scriptClock) stop() {
	c.mu.Lock()
	c.timer.Stop()
	c.mu.Unlock()
}

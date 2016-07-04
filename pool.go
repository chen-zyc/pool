package pool

import (
	"container/list"
	"errors"
	"sync"
	"time"
)

var nowFunc = time.Now // for test

var (
	ErrPoolClosed    = errors.New("pool closed")
	ErrPoolExhausted = errors.New("pool exhausted")
)

type Pool struct {
	New          func() (interface{}, error)
	TestOnBorrow func(interface{}) error
	DropCallback func(interface{}) // 丢弃对象的回调
	MaxIdle      int
	MaxActive    int
	IdleTimeout  time.Duration
	Wait         bool // 如果为true，当pool达到MaxActive后，会等待一个对象返回到pool中
	mu           sync.Mutex
	cond         *sync.Cond
	closed       bool
	active       int
	idle         list.List
}

type idleObj struct {
	obj interface{}
	t   time.Time
}

func NewPool(New func() (interface{}, error), maxIdle int) *Pool {
	return &Pool{
		New:     New,
		MaxIdle: maxIdle,
	}
}

func (p *Pool) Get() (interface{}, error) {
	p.mu.Lock()

	drop := p.DropCallback
	// 清除过期的对象
	if timeout := p.IdleTimeout; timeout > 0 {
		for i, n := 0, p.idle.Len(); i < n; i++ {
			e := p.idle.Back() // 最旧的那个
			if e == nil {
				break
			}
			io := e.Value.(idleObj)
			if io.t.Add(timeout).After(nowFunc()) {
				break // 最旧的那个都没有过期，其他的也不会过期
			}
			// 清除过期的
			p.idle.Remove(e)
			p.release()
			if drop != nil {
				p.mu.Unlock()
				drop(io.obj)
				p.mu.Lock()
			}
		}
	}

	// 获取空闲对象
	for {
		for i, n := 0, p.idle.Len(); i < n; i++ {
			e := p.idle.Front() // 最新的
			if e == nil {
				break
			}
			io := e.Value.(idleObj)
			p.idle.Remove(e)

			test := p.TestOnBorrow
			p.mu.Unlock()
			if test == nil || test(io.obj) == nil {
				return io.obj, nil
			}
			// 这个对象不可用了，丢掉
			if drop != nil {
				drop(io.obj)
			}
			p.mu.Lock()
			p.release()
		}

		// 在创建新对象前检查是否关闭
		if p.closed {
			p.mu.Unlock()
			return nil, ErrPoolClosed
		}

		if p.MaxActive == 0 || p.active < p.MaxActive {
			newFunc := p.New
			p.active++
			p.mu.Unlock()
			obj, err := newFunc()
			if err != nil {
				p.mu.Lock()
				p.release()
				p.mu.Unlock()
				obj = nil
			}
			return obj, err
		}

		if !p.Wait { // 不等待
			p.mu.Unlock()
			return nil, ErrPoolExhausted
		}

		if p.cond == nil {
			p.cond = sync.NewCond(&p.mu)
		}
		p.cond.Wait()
	}
}

func (p *Pool) Put(obj interface{}) {
	p.mu.Lock()

	if !p.closed {
		p.idle.PushFront(idleObj{
			t:   nowFunc(),
			obj: obj,
		})
		if p.idle.Len() > p.MaxIdle {
			obj = p.idle.Remove(p.idle.Back()).(idleObj).obj
		} else {
			if p.cond != nil {
				p.cond.Signal()
			}
			p.mu.Unlock()
			return
		}
	}

	p.release()
	drop := p.DropCallback
	p.mu.Unlock()
	if drop != nil {
		drop(obj)
	}
	return
}

func (p *Pool) ActiveCount() int {
	p.mu.Lock()
	active := p.active
	p.mu.Unlock()
	return active
}

func (p *Pool) Close() {
	p.mu.Lock()
	idle := p.idle
	p.idle.Init()
	p.closed = true
	p.active -= idle.Len()
	if p.cond != nil {
		p.cond.Broadcast()
	}
	drop := p.DropCallback
	p.mu.Unlock()

	if drop == nil {
		return
	}
	for e := idle.Front(); e != nil; e = e.Next() {
		drop(e.Value.(idleObj).obj)
	}
}

func (p *Pool) release() {
	p.active--
	if p.cond != nil {
		p.cond.Signal()
	}
}

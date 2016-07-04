package pool

import (
	"errors"
	"testing"
	"time"
)

type conn struct {
}

type poolDialer struct {
	t      *testing.T
	dialed int // 连接了多少次
	open   int // 打开状态的连接
}

func (d *poolDialer) dial() (interface{}, error) {
	d.dialed++
	d.open++
	return &conn{}, nil
}

func (d *poolDialer) drop(interface{}) {
	d.open--
}

func (d *poolDialer) check(message string, p *Pool, dialed, open int) {
	if d.dialed != dialed {
		d.t.Errorf("%s: dialed=%d, want %d", message, d.dialed, dialed)
	}
	if d.open != open {
		d.t.Errorf("%s: open=%d, want %d", message, d.open, open)
	}
	if active := p.ActiveCount(); active != open {
		d.t.Errorf("%s: active=%d, want %d", message, active, open)
	}
}

func TestPoolReuse(t *testing.T) {
	d := &poolDialer{t: t}
	p := NewPool(d.dial, 2)
	p.DropCallback = d.drop

	for i := 0; i < 10; i++ {
		o1, err := p.Get()
		if err != nil {
			t.Fatal(err)
		}
		o2, err := p.Get()
		if err != nil {
			t.Fatal(err)
		}
		p.Put(o1)
		p.Put(o2)
	}

	d.check("before close", p, 2, 2)
	p.Close()
	d.check("after close", p, 2, 0)
}

func TestPoolMaxIdle(t *testing.T) {
	d := &poolDialer{t: t}
	p := NewPool(d.dial, 2)
	p.DropCallback = d.drop

	for i := 0; i < 10; i++ {
		o1, err := p.Get()
		if err != nil {
			t.Fatal(err)
		}
		o2, err := p.Get()
		if err != nil {
			t.Fatal(err)
		}
		o3, err := p.Get()
		if err != nil {
			t.Fatal(err)
		}
		p.Put(o1)
		p.Put(o2)
		p.Put(o3)
	}

	d.check("before close", p, 12, 2) // 12 = 3 + 1 * 9
	p.Close()
	d.check("after close", p, 12, 0)
}

func TestPoolClose(t *testing.T) {
	d := &poolDialer{t: t}
	p := NewPool(d.dial, 2)
	p.DropCallback = d.drop

	o1, _ := p.Get()
	o2, _ := p.Get()
	o3, _ := p.Get()

	p.Put(o1)
	p.Put(o2)

	p.Close()
	d.check("after pool close", p, 3, 1)

	p.Put(o3)
	d.check("after conn close", p, 3, 0)

	o4, err := p.Get()
	if err == nil || o4 != nil {
		t.Errorf("expected error after pool closed")
	}
}

func TestPoolTimeout(t *testing.T) {
	d := &poolDialer{t: t}
	p := NewPool(d.dial, 2)
	p.DropCallback = d.drop
	p.IdleTimeout = time.Second

	now := time.Now()
	nowFunc = func() time.Time {
		return now
	}
	defer func() {
		nowFunc = time.Now
	}()

	o, err := p.Get()
	if err != nil {
		t.Fatal(err)
	}
	p.Put(o)
	d.check("1", p, 1, 1)

	now = now.Add(time.Second)

	o, err = p.Get()
	if err != nil {
		t.Fatal(err)
	}
	p.Put(o)
	d.check("2", p, 2, 1)
	p.Close()
}

func TestPoolBorrowCheck(t *testing.T) {
	d := &poolDialer{t: t}
	p := NewPool(d.dial, 2)
	p.DropCallback = d.drop
	p.TestOnBorrow = func(o interface{}) error {
		return errors.New("err")
	}

	for i := 0; i < 10; i++ {
		o, err := p.Get()
		if err != nil {
			t.Fatal(err)
		}
		p.Put(o)
	}

	d.check("1", p, 10, 1)
	p.Close()
}

func TestPoolMaxActive(t *testing.T) {
	d := &poolDialer{t: t}
	p := NewPool(d.dial, 2)
	p.DropCallback = d.drop
	p.MaxActive = 2
	p.MaxIdle = 2

	p.Get()
	o2, _ := p.Get()
	d.check("1", p, 2, 2)

	_, err := p.Get()
	if err != ErrPoolExhausted {
		t.Errorf("expected pool exhausted")
	}

	d.check("2", p, 2, 2)

	p.Put(o2)
	d.check("3", p, 2, 2)

	o3, _ := p.Get()
	p.Put(o3)

	d.check("4", p, 2, 2)
	p.Close()
}

func startGroutines(p *Pool) chan error {
	errs := make(chan error, 10)
	for i := 0; i < 10; i++ {
		go func() {
			o, err := p.Get()
			if err == nil && o == nil {
				err = errors.New("nil object")
			}
			errs <- err
			if o != nil {
				p.Put(o)
			}
		}()
	}
	// wait for goroutines to block
	time.Sleep(time.Second / 4)
	return errs
}

func TestWaitPool(t *testing.T) {
	d := &poolDialer{t: t}
	p := &Pool{
		New:       d.dial,
		MaxIdle:   1,
		MaxActive: 1,
		Wait:      true,
	}
	defer p.Close()

	o, _ := p.Get()
	errs := startGroutines(p)
	d.check("before put", p, 1, 1)
	p.Put(o)

	timeout := time.After(2 * time.Second)
	for i := 0; i < cap(errs); i++ {
		select {
		case err := <-errs:
			if err != nil {
				t.Fatal(err)
			}
		case <-timeout:
			t.Fatalf("timeout waiting for blocked goroutine %d", i)
		}
	}
	d.check("done", p, 1, 1)
}

func TestWaitPoolClose(t *testing.T) {
	d := &poolDialer{t: t}
	p := &Pool{
		New:          d.dial,
		MaxIdle:      1,
		MaxActive:    1,
		Wait:         true,
		DropCallback: d.drop,
	}

	o, _ := p.Get()
	errs := startGroutines(p)
	d.check("before put", p, 1, 1)

	p.Close()

	timeout := time.After(2 * time.Second)
	for i := 0; i < cap(errs); i++ {
		select {
		case err := <-errs:
			switch err {
			case nil:
				t.Fatal("blocked goroutine did not get error")
			case ErrPoolExhausted:
				t.Fatal("blocked goroutine got pool exhausted error")
			}
		case <-timeout:
			t.Fatalf("timeout waiting for blocked goroutine %d", i)
		}
	}
	p.Put(o)
	d.check("done", p, 1, 0)
}

func BenchmarkPoolGet(b *testing.B) {
	b.StopTimer()
	p := &Pool{
		New: func() (interface{}, error) {
			return &conn{}, nil
		},
		MaxIdle: 2,
	}
	defer p.Close()
	o, err := p.Get()
	if err != nil {
		b.Fatal(err)
	}
	p.Put(o)

	b.StartTimer()

	for i := 0; i < b.N; i++ {
		o, err := p.Get()
		if err == nil && o != nil {
			p.Put(o)
		} else {
			b.Fatal("err:", err, "obj:", o)
		}
	}
}

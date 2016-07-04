# pool
rewrite from https://github.com/garyburd/redigo/blob/master/redis/pool.go

类似与sync.Pool，但可以设置更多参数，原本是用于redigo中的连接池的。

# 使用

## 简单使用

```go
p := NewPool(newFunc, 2)
obj, err := p.Get()
if err != nil {
	log.Fatal(err)
}
defer p.Put(obj)
...
```

## Pool中字段含义

* New func()(interface{}, error): 当没有空闲对象时，用于创建对象，当返回error时,Get()也会返回同样的error
* MaxIdle int: 可保存的最大空闲对象数
* IdleTimeout time.Duration: 空闲对象的超时时间
* MaxActive int: 最大活跃对象，当活跃对象超出该限制时，行为视Wait参数而定
* Wait bool: 当为true时，如果没有空闲对象，会阻塞Get()方法，直到有可用对象为止。当为false时，如果没有空闲对象，返回ErrPoolExhausted错误。
* DropCallback func(interface{}): 当对象被从队列中删除时调用的方法。
* TestOnBorrow func(interface{}) error: 当对象从空闲队列中取出时调用的方法，若该方法返回错误，取出的对象会被丢弃，然后重新获取，直到该方法返回nil或者没有空闲对象为止。
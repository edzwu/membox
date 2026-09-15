+++
title = "DCLP（Double-Checked Locking Pattern）"
date = "2026-01-14T15:37:45Z"
+++

> 关键词：内存模型 / happens-before / critical section / 单例模式  
> **背景场景 → 问题定义 → DCLP 解法 → 现代写法（call_once / magic static）**。

---

## idea

DCLP 的核心直觉其实很朴素：

- 很多系统里存在 **快路径 / 慢路径**：
  - **慢路径**只在“第一次/偶尔”发生（初始化、构建缓存、加载配置、构建索引…）
  - **快路径**在热路径里被反复调用（每次调用都要拿到那个对象/缓存/表…）
- 需要：
  - 第一次：允许慢，但必须 **只做一次**
  - 之后：尽量不加锁，走 **无锁快路径**
- DCLP 的套路就是：
  1) 先无锁检查一次（走快路径）
  2) 只有“看起来需要初始化”才加锁进入慢路径
  3) 锁内再检查一次（避免重复初始化）
  4) 初始化完成后，把“对象已经可用”安全发布给其它线程

**一句话**：DCLP 是一种“减少锁竞争”的模式，但它的正确性取决于**内存模型（happens-before）**。

---

## Background && Motivation

### 背景：哪些地方会出现“快慢系统”？

1) **单例/全局资源**（配置中心、日志器、metrics registry、连接池…）  
   - 每次业务调用都要访问它 → 热路径  
   - 初始化只做一次 → 慢路径

2) **懒加载缓存（lazy cache）**  
   - 第一次请求时构建大对象（索引、词典、regex 编译、模型加载…）
   - 后续请求只读缓存

3) **一次性初始化的昂贵数据结构**  
   - 例如：解析配置文件 → 生成路由表 → 构建 hash 索引 → 发布给读线程  
   - 读线程频繁读取，写线程几乎不再写

这些场景里，如果每次都 `lock()`，就把“慢路径的成本”扩散到了整个系统：  
**明明只有第一次需要慢，结果每次都付锁的成本。**

---

### problem defined

为什么需要 DCLP？因为往往同时面对两类问题：

#### 问题1：性能问题（每次都锁太贵）

“只初始化一次”的最直接写法是：

```cpp
T* p = nullptr;
std::mutex m;

T* get() {
  std::lock_guard<std::mutex> lk(m);
  if (!p) p = new T();
  return p;
}
```

缺点很直观：  
**每次调用都要锁**，哪怕 `p` 早就初始化完了。

当 `get()` 是极热路径（每个请求、每个日志、每帧处理…）时，这个锁可能成为吞吐瓶颈。

---

#### 问题2：正确性问题（“发布指针” != “发布完整对象状态”）

并不是 “锁内 new 一次” 就完了，DCLP 最坑的地方是：

> 另一个线程可能看到 `p != nullptr`，却看到的是一个**尚未构造完成**（或构造写入尚不可见）的对象。

因为：

```cpp
p = new T();
```

在抽象层面可拆成三步：

1) 分配内存 `mem = operator new(sizeof(T))`
2) 在 `mem` 上构造对象（写入字段）
3) 把指针写入 `p`（对外发布）

如果没有正确同步，别的线程的“观察顺序”可能是：

```text
Thread A: allocate mem
Thread A: p = mem              (p 已非空，被别人看见)
Thread A: construct T in mem   (对象字段还在写，或写入尚不可见)

Thread B: sees p != nullptr
Thread B: uses *p              (读到半初始化状态 → UB 风险)
```

这就是为什么 “DCLP 不只是加两次 if”——它本质上是一个**内存可见性 / happens-before**问题。
也就是说, 变量不仅要保证自己不发生 data race 的问题, 也要保证 happend-before 的内存模型
需要维护附近的变量更新, 在另一个线程内的可见性

---

## Solution

### DCLP 是什么？（模式定义）

DCLP 的经典形态如下：

```cpp
if (p == nullptr) {       // (A) 快路径：无锁检查
  lock();
  if (p == nullptr) {     // (B) 慢路径：锁内二次确认
    p = new T();
  }
  unlock();
}
return p;
```

两次检查的职责不同：

- 外层 if：**性能**（避免每次都锁）
- 内层 if：**正确性**（避免重复初始化；典型 TOCTTOU：check 与 use 之间状态可能变化）

到这里为止，这只是“逻辑层面”的 DCLP。  
真正难点是：**如何保证 (B) 初始化完成后，(A) 快路径读到的是“完整对象”？**

真正需要的是下面两种之一：

- 用 **mutex** 把“写入/读取”放入同一个同步域（简单稳）
- 或者用 **atomic（acquire/release）** 把“发布/获取”关系写清楚（快路径无锁）

---

### 正确的 DCLP（C++11+）：atomic + acquire/release + mutex

如果真的需要 DCLP（快路径无锁），推荐写成“发布-获取”模型：

- 发布者（写线程）：构造完成后 `store(..., release)` 发布指针
- 读者（读线程）：`load(..., acquire)` 获取指针；一旦读到非空，就保证能看到构造写入

示例：

```cpp
#include <atomic>
#include <mutex>

struct T {
  int a = 1;
  // ... 复杂初始化 ...
};

std::atomic<T*> p{nullptr};
std::mutex m;

T* instance() {
  // 1) 快路径：acquire 读
  T* tmp = p.load(std::memory_order_acquire);
  if (tmp == nullptr) {
    // 2) 慢路径：锁内初始化
    std::lock_guard<std::mutex> lk(m);

    // 锁内再读一次：此处用 relaxed 即可（锁本身提供同步）
    tmp = p.load(std::memory_order_relaxed);
    if (tmp == nullptr) {
      tmp = new T();

      // 3) 发布：release store
      p.store(tmp, std::memory_order_release);
    }
  }
  return tmp;
}
```

可以这样理解这段代码在“语义层面”保证了什么：

- `store(release)`：把构造期间对 `T` 内部字段的写入，“排在指针发布之前”
- `load(acquire)`：一旦读到非空指针，就“保证能看到发布前的写入”，从而读到完整对象状态

---

### 现代写法：`std::call_once`（推荐）

DCLP 的最大问题不是“难懂”，而是“太容易被写错（尤其内存序）”。  
现代 C++ 给了直接表达意图的工具：**一次性初始化**。

```cpp
#include <mutex>

struct T { /* ... */ };

std::once_flag flag;
T* p = nullptr;

T* instance() {
  std::call_once(flag, [] {
    p = new T();
  });
  return p;
}
```

优点：

- 意图清晰：**只执行一次**
- 不需要自己推导“发布/获取”的内存序组合
- 对阅读代码的人非常友好

---

### 现代写法：magic static（Meyers Singleton，最简）

如果接受对象生命周期“到进程结束”，那么这可能是最爽的写法：

```cpp
T& instance() {
  static T obj; // C++11 起保证初始化线程安全
  return obj;
}
```

优点：短、稳、表达力强。  
缺点：生命周期固定、参数化初始化不方便、测试替换能力较弱。

---


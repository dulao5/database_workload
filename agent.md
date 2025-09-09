# 功能需求

这是一个具有一定通用功能的 Database workload 模拟器。
（只负责跑 SQL，至于数据的生成由另一个项目 dbgen 负责）

## 1. 并发控制
* 支持配置并发 worker 数量
* 每个 worker 独立运行压测循环
* 所有 worker 同时启动，持续运行直到手动停止

## 2. 数据库连接管理
* 每次压测循环都新建数据库连接
* 不使用连接池，确保每次都是全新的连接
* 执行完成后立即关闭连接
* 支持 MySQL 数据库
```
db, err := sql.Open("mysql", dsn)
db.SetMaxOpenConns(1)
db.SetMaxIdleConns(0)
conn = db.Conn(...)
...
conn.Close()
```

## 3. SQL模板系统
* 从配置文件加载SQL模板
* 每个模板包含一个SQL语句和参数定义
* 每个 Session 支持多个模板按顺序执行
* 参数支持动态生成
* 支持将这些 SQL 放在 begin/commit 内执行（可配置）

## 4. 参数随机化
参数支持以下类型和随机模式：

### 数字类型

* 均匀分布：在指定范围内均匀随机
    * 配置：min, max (int64)
* 幂律分布：在指定范围内按幂律分布随机
    * 配置：min, max (int64), exponent (float64)
* 分区幂律分布：在指定范围内，先按照 partition 数分区，先均匀随机选一个分区，然后在此分区内按幂律分布随机
    * 配置：min, max (int64), exponent (float64), partition(int64)
* 集合分布：从预定义值中按概率随机选择
    * 配置：值到权重的映射


### 字符串类型
* 数字格式化：基于数字随机值格式化生成
    * 先基于上述“数字类型的随机量”，随机生成数字
    * 再根据配置的format字符串, 生成字符串
* 数字Hash
    * 先基于上述“数字类型的随机量”，随机生成数字
    * 再根据配置的 hash function 类型, 生成字符串
* 随机字符串：从字符范围内(eg.a-zA-Z0-9)，随机返回指定长度的字符串
* 集合分布：从预定义字符串中按概率随机选择
    * 配置：字符串到权重的映射

### 日期类型
* 时间戳范围：在指定时间范围内均匀随机
    * 配置：开始时间, 结束时间
* 日期格式化：基于时间戳随机值格式化生成
    * 配置：format字符串, 时间戳范围配置


### 数组类型
* 元素是 数字类型 和 字符串类型 和 日期类型 的数组
* 根据指定 array size，生成 array
* 一般是生成到 where 条件里的 `in ()` 里面


# 5. 幂律分布算法

使用真正的幂律分布算法：
* 概率密度函数：p(x) ∝ x^(-α)
* 使用逆变换采样方法生成随机数
* 支持整数范围内的幂律分布
* 对于“分区幂律分布”，是指先将 `min~max` 的整数范围 生成 `partition` 个分区，均匀随机选一个分区，然后再该分区范围内使用幂律分布算法
  * 适用于模拟表很大，热点现象不突出，但是每个分区内被访问的 id 有重复的情况

# 配置文件格式

```
{
  "concurrency": 100,
  "db_conn_str": "mysql://user:password@tcp(host:port)/dbname",
  "templates": [
    {
      "sql": "INSERT INTO table (id, name, value, created_at) VALUES (?, ?, ?, ?)",
      "params": [
        {
          "type": "number",
          "random_mode": "uniform",
          "min": 1,
          "max": 1000
        },
        {
          "type": "string",
          "random_mode": "number_format",
          "format": "user_%d",
          "number_config": {
            "random_mode": "power_law",
            "min": 10000000000,
            "max": 14400000000,
            "exponent": 2.5
          }
        },
        {
          "type": "number",
          "random_mode": "power_law",
          "min": 1,
          "max": 1000,
          "exponent": 1.5
        },
        {
          "type": "date",
          "random_mode": "timestamp_range",
          "start_time": "2023-01-01T00:00:00Z",
          "end_time": "2023-12-31T23:59:59Z",
          "format": "2006-01-02 15:04:05"
        }
      ]
    },
    {
      "sql": "SELECT * FROM table WHERE category = ? AND id IN (?)",
      "params": [
        {
          "type": "string",
          "random_mode": "set",
          "set_mode": "weighted",
          "values": {
            "cat1": 0.6,
            "cat2": 0.3,
            "cat3": 0.1
          }
        },
        {
          "type": "array",
          "array_size": 5,
          "element_type": "number",
          "element_config": {
            "random_mode": "uniform",
            "min": 1,
            "max": 1000
          }
        }
      ]
    },
    {
      "sql": "UPDATE table SET status = ? WHERE id IN (?)",
      "params": [
        {
          "type": "string",
          "random_mode": "set",
          "set_mode": "uniform",
          "values": ["active", "inactive", "pending"]
        },
        {
          "type": "array",
          "array_size": 3,
          "element_type": "string",
          "element_config": {
            "random_mode": "number_format",
            "format": "ID_%d",
            "number_config": {
              "random_mode": "power_law",
              "min": 1000,
              "max": 10000,
              "exponent": 2.0
            }
          }
        }
      ]
    }
  ]
}

```
# 单元测试要求

1. 配置解析模块测试
* 测试有效配置的正确解析
* 测试无效配置的适当错误处理
* 测试默认值的正确设置
* 测试各种参数类型的配置解析

2. 随机数生成器测试

均匀分布生成器
* 测试生成值在指定范围内
* 测试分布的大致均匀性（通过统计检验）
* 测试边界情况（min = max）

幂律分布生成器
* 测试生成值在指定范围内
* 测试不同指数下的分布特性
* 验证分布符合幂律特性（通过统计检验）
* 测试边界情况（指数接近1时的处理）

分区幂律分布生成器
* 测试生成值在指定范围内
* 测试不同指数下的分布特性
* 测试不同分区下的数据分布
* 验证分布符合幂律特性（通过统计检验）
* 测试边界情况（指数接近1时的处理）


集合分布生成器
* 测试加权随机选择符合指定概率
* 测试均匀随机选择大致均匀
* 测试空集合和单元素集合的特殊情况

字符串格式化器
* 测试数字到字符串的正确格式化
* 测试日期到字符串的正确格式化
* 测试各种格式字符串的处理

3. 数据库连接测试
* 测试连接建立和关闭的正确性
* 测试SQL执行和参数绑定的正确性
* 测试错误处理（连接失败、SQL错误等）

4. 并发控制测试
* 测试正确数量的worker启动
* 测试worker的独立运行
* 测试并发安全性（无数据竞争）

5. 集成测试
* 测试完整流程：配置加载、worker启动、数据库操作
* 测试性能指标收集和报告
* 测试错误处理和恢复机制

# 实现注意事项
1. 性能优化：
    * 幂律分布生成器预计算常数
    * 每个worker使用独立的随机数生成器
    * 避免在循环中进行昂贵计算
2. 错误处理：
    * 数据库连接失败时记录错误并继续
    * SQL执行错误时记录错误并继续
    * 配置错误时立即终止并报告
3. 可扩展性：
    * 设计接口以便添加新的随机模式
    * 设计接口以便添加新的参数类型
4. 统计收集：
    * 收集执行次数、成功次数、错误次数
    * 收集执行时间分布
    * 提供实时统计报告

# 使用示例

1. 创建配置文件 config.json
2. 运行工具：./database-workload-test -config config.json
3. 查看实时统计输出
4. 使用Ctrl+C停止测试

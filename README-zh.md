# 数据库工作负载生成器

[English](README.md) | [日本語](README-ja.md)

一个灵活的数据库基准测试工作负载生成器，支持多种数据类型和分布模式。

## 功能特性

- 多种数据类型生成器：
  - 数字（均匀/幂律/分区分布）
  - 字符串（格式化数字、加权/均匀集合）
  - 日期（带自定义格式的时间戳范围）
  - 数组（可配置元素的复合类型）

- 可配置的数据分布：
  - 数字的均匀分布
  - 幂律分布
  - 分区幂律
  - 加权随机选择
  - 基于时间范围的生成

## 使用方法

### 配置文件

在 `config.json` 中配置工作负载：

```json
{
  "concurrency": 10,
  "rate_per_thread": 100,
  "db_conn_str": "user:password@tcp(127.0.0.1:4000)/dbname?parseTime=true",
  "use_transaction": true,
  "connection_type": "long",
  "templates": [
    {
      "sql": "INSERT INTO users (id, name, created_at, tags) VALUES (?, ?, ?, ?)",
      "params": [
        {
          "type": "number",
          "random_mode": "uniform",
          "min": 1,
          "max": 1000000
        },
        {
          "type": "string",
          "random_mode": "number_format",
          "format": "user_%d",
          "number_config": {
            "random_mode": "uniform",
            "min": 1,
            "max": 1000
          }
        },
        {
          "type": "date",
          "random_mode": "range",
          "start": "2023-01-01T00:00:00Z",
          "end": "2023-12-31T23:59:59Z",
          "format": "2006-01-02 15:04:05"
        }
      ]
    },
    {
      "sql": "SELECT * FROM users WHERE id = ?",
      "params": [
        {
          "type": "number",
          "random_mode": "power_law",
          "min": 1,
          "max": 1000000,
          "exponent": 2.0
        }
      ]
    },
    {
      "sql": "SELECT * FROM users WHERE id = ?",
      "params": [
        {
          "type": "number",
          "random_mode": "partition_power_law",
          "min": 1,
          "max": 100000000,
          "exponent": 2.0,
          "partition": 2000
        }
      ]
    },
    {
      "sql": "SELECT * FROM users WHERE id in (?)",
      "params": [
        {
          "type": "array",
          "array_size": 4,
          "element_type": "number",
          "element_config": {
            "random_mode": "partition_power_law",
            "min": 1,
            "max": 100000000,
            "exponent": 1.001,
            "partition": 2000
          }
        }
      ]
    },
    {
      "sql": "SELECT * FROM sbtest1 WHERE c in (?)",
      "params": [
        {
          "type": "array",
          "array_size": 4,
          "element_type": "string",
          "element_config": {
            "type": "string",
            "random_mode": "number_format",
            "format": "abc_%d",
            "number_config": {
              "random_mode": "partition_power_law",
              "min": 1,
              "max": 100000000,
              "exponent": 1.001,
              "partition": 2000
            }
          }
        }
      ]
    }
  ]
}
```

### 运行命令

```bash
database_workload -config config.json
```

## 参数类型参考

### 1. 数字生成器

```json
```json
{
  "type": "number",
  "random_mode": "uniform",
  "min": 1,
  "max": 1000000
}
```
```json
{
  "type": "number",
  "random_mode": "power_law",
  "min": 1,
  "max": 1000000,
  "exponent": 1.01  // for power_law distribution
}
```
```json
{
  "type": "number",
  "random_mode": "partition_power_law",
  "min": 1,
  "max": 1000000,
  "exponent": 1.01,  // for power_law distribution in one partition
  "partition": 100   // number of partitions
}
```


### 2. 字符串生成器

```json
{
  "type": "string",
  "random_mode": "set",         // "set"(集合), "number_format"(数字格式化)
  "set_mode": "weighted",       // "weighted"(加权), "uniform"(均匀)
  "values": {
    "value1": 0.7,
    "value2": 0.3
  }
}
```
```json
{
  "type": "string",
  "random_mode": "number_format",
  "format": "abc_%d",
  "number_config": {
    "random_mode": "partition_power_law",
    "min": 1,
    "max": 100000000,
    "exponent": 1.001,
    "partition": 2000
  }
}
```

### 3. 日期生成器

```json
{
  "type": "date",
  "random_mode": "range",
  "start": "2023-01-01T00:00:00Z",
  "end": "2023-12-31T23:59:59Z",
  "format": "2006-01-02 15:04:05"
}
```

### 4. 数组生成器

```json
{
  "type": "array",
  "array_size": 4,
  "element_type": "number",
  "element_config": {
    "random_mode": "partition_power_law",
    "min": 1,
    "max": 100000000,
    "exponent": 1.001,
    "partition": 2000
  }
}
```
5. **数组生成器(随机数字的格式化字符串)**:
```json
{
    "type": "array",
    "array_size": 4,
    "element_type": "string",
    "element_config": {
      "type": "string",
      "random_mode": "number_format",
      "format": "abc_%d",
      "number_config": {
          "random_mode": "partition_power_law",
          "min": 1,
          "max": 100000000,
          "exponent": 1.001,
          "partition": 2000
      }
    }
}
```

## Sysbench OLTP 配置文件

仓库里自带了几个现成的`sysbench-config-*.json`配置文件（加上更早期的`config.json`/`sysbench-config.json`），每一个都对应一种不同的执行路径，可以在同样的并发/吞吐下互相对比压测。"完整版"oltp-read-write系列（4种SELECT变体+2个UPDATE+DELETE+INSERT，照搬sysbench自己的`oltp_read_write.lua`）才是真正用于每一轮fix-prepared/multi-statement/pipelined-binary对比测试的那一套；`*-prepared*`/`sysbench-config.json`/`config.json`这几个文件是更早期、更小的原型，留着只是方便本地快速冒烟测试，不用于正式的头对头对比。

| 文件 | 模板 | 执行模式 | 相对baseline多开的flag |
|---|---|---|---|
| `sysbench-config-oltp-read-write.json` | 完整oltp-read-write（10个模板） | 普通的`database/sql`prepared statement，走`(*sql.Tx).StmtContext`——这是"朴素"baseline，会悄悄每次都重新PREPARE（见`config/config.go`里`FixPreparedStatementReuse`的doc comment），所以**不等于**benchmark里用作对照的那个"fix-prepared"baseline | `use_prepared_statements:true`，`multi_statements:false` |
| `sysbench-config-oltp-read-write-fixprepared.json` | 同上 | **fix-prepared**：每条语句只PREPARE一次，整条连接生命周期内复用（绕开上面那个stdlib `Tx.StmtContext`重新PREPARE的行为）——这是其他所有模式对比时用的baseline | 加了`fix_prepared_statement_reuse:true` |
| `sysbench-config-oltp-read-write-multi.json` | 同上 | **multi-statement**：整个事务走`tidb-multistmt`的一次文本协议往返（`SET`标记位+`EXECUTE ps USING ...`） | `multi_statements:true` |
| `sysbench-config-oltp-read-write-pipelined.json` | 同上 | **binary-multistmt（pipelined）**：整个事务经由`github.com/dulao5/tidb-binary-multistmt`pipeline发送——二进制`COM_STMT_EXECUTE`包连续写完，一次往返，没有文本协议的`SET`标记 | 加了`pipelined_binary:true`（`multi_statements`为保持一致也设了true，但这个模式下会被忽略——见`PipelinedBinary`的doc comment） |
| `sysbench-config-prepared.json` | 精简版2模板原型（只有选表+批量point-select，早于完整sysbench移植版本） | 普通prepared statement（跟上面完整版baseline同样的坑） | `use_prepared_statements:true`，`multi_statements:false` |
| `sysbench-config-prepared-multi.json` | 同上精简模板 | multi-statement批处理 | `multi_statements:true` |
| `sysbench-config.json` | 同上精简模板 | 纯文本协议查询，完全不用prepared statement | （无） |
| `config.json` | 单个`WHERE id IN (?)`模板 | 纯文本协议查询；上面"使用方法"一节的示例就是这个文件，演示`array`类型的参数生成器 | （无） |

## 操作系统调优（高QPS场景下，connection_type为"short"时）
```
sysctl -w net.ipv4.ip_local_port_range="1024 65535"
sysctl -w net.ipv4.tcp_tw_reuse=1
sysctl -w net.ipv4.tcp_fin_timeout=10
sysctl -w net.netfilter.nf_conntrack_max=1048576
sysctl -w net.netfilter.nf_conntrack_buckets=262144

ulimit -n 1048576
echo "* soft nofile 1048576" >> /etc/security/limits.conf
echo "* hard nofile 1048576" >> /etc/security/limits.conf

# 追加到 /etc/systemd/system.conf 和 /etc/systemd/user.conf
DefaultLimitNOFILE=1048576
# 重启 systemd-logind：
systemctl restart systemd-logind
```